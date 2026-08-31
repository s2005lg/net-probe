package command

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
)

type State string

const (
	Queued     State = "queued"
	Dispatched State = "dispatched"
	Accepted   State = "accepted"
	Running    State = "running"
	Succeeded  State = "succeeded"
	Failed     State = "failed"
	Expired    State = "expired"
)

type Store struct {
	db     *sql.DB
	signer ed25519.PrivateKey
	mu     sync.Mutex
	now    func() time.Time
}

type HistoryItem struct {
	Command      controlproto.Command `json:"command"`
	State        State                `json:"state"`
	DispatchedAt int64                `json:"dispatched_at"`
	AcceptedAt   int64                `json:"accepted_at"`
	StartedAt    int64                `json:"started_at"`
	FinishedAt   int64                `json:"finished_at"`
	AttemptCount int                  `json:"attempt_count"`
	ResultCode   string               `json:"result_code"`
	Result       json.RawMessage      `json:"result"`
}

func NewStore(db *sql.DB, signer ed25519.PrivateKey) (*Store, error) {
	if db == nil || len(signer) != ed25519.PrivateKeySize {
		return nil, errors.New("command store requires a database and Ed25519 private key")
	}
	return &Store{db: db, signer: ed25519.PrivateKey(append([]byte(nil), signer...)), now: time.Now}, nil
}

func (s *Store) Create(ctx context.Context, actor auth.Actor, agentID string, action controlproto.Action, payload []byte) (controlproto.Command, error) {
	if actor.UserID <= 0 || agentID == "" {
		return controlproto.Command{}, errors.New("command actor and Agent ID are required")
	}
	ttl := controlproto.TTL(action)
	if ttl <= 0 {
		return controlproto.Command{}, errors.New("unsupported command action")
	}
	if len(payload) == 0 || len(payload) > controlproto.MaxMessageBytes || !json.Valid(payload) {
		return controlproto.Command{}, errors.New("command payload must be bounded valid JSON")
	}
	commandID, err := commandUUID()
	if err != nil {
		return controlproto.Command{}, err
	}
	now := s.now().UTC().Truncate(time.Second)

	// A single Panel owns this SQLite database. Serializing allocation avoids
	// SQLite lock churn while the transaction preserves sequence/signature/event
	// atomicity across crashes.
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return controlproto.Command{}, err
	}
	defer tx.Rollback()
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM agent_commands WHERE agent_id=?`, agentID).Scan(&sequence); err != nil {
		return controlproto.Command{}, err
	}
	command := controlproto.Command{
		ControlVersion: controlproto.Version, Type: "command", CommandID: commandID, Sequence: sequence,
		AgentID: agentID, Action: action, IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(),
		Payload: append(json.RawMessage(nil), payload...),
	}
	if err := controlproto.SignCommand(s.signer, &command); err != nil {
		return controlproto.Command{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_commands(command_id,agent_id,sequence,control_version,message_type,action,payload,signature,state,issued_at,expires_at,created_by_user_id,created_session_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		command.CommandID, command.AgentID, command.Sequence, command.ControlVersion, command.Type, command.Action, string(command.Payload), command.Signature,
		Queued, command.IssuedAt, command.ExpiresAt, actor.UserID, actor.SessionID); err != nil {
		return controlproto.Command{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_command_events(command_id,actor_id,session_id,from_state,to_state,created_at,reason_code) VALUES(?,?,?,?,?,?,?)`,
		command.CommandID, actor.UserID, actor.SessionID, "", Queued, now.Unix(), "created"); err != nil {
		return controlproto.Command{}, err
	}
	if err := tx.Commit(); err != nil {
		return controlproto.Command{}, err
	}
	return command, nil
}

func (s *Store) Transition(ctx context.Context, commandID string, to State, actor auth.Actor, reasonCode, resultCode string, resultJSON []byte) error {
	return s.transitionAt(ctx, commandID, to, actor, reasonCode, resultCode, resultJSON, s.now().UTC().Truncate(time.Second))
}

func (s *Store) transitionAt(ctx context.Context, commandID string, to State, actor auth.Actor, reasonCode, resultCode string, resultJSON []byte, now time.Time) error {
	if commandID == "" || len(reasonCode) > 128 || len(resultCode) > 128 || len(resultJSON) > 16*1024 {
		return errors.New("invalid command transition fields")
	}
	if len(resultJSON) == 0 {
		resultJSON = []byte(`{}`)
	}
	if !json.Valid(resultJSON) {
		return errors.New("command result must be valid JSON")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from State
	if err := tx.QueryRowContext(ctx, `SELECT state FROM agent_commands WHERE command_id=?`, commandID).Scan(&from); err != nil {
		return err
	}
	if !allowedTransition(from, to) {
		return fmt.Errorf("forbidden command transition %s -> %s", from, to)
	}
	timestamp := now.Unix()
	result, err := tx.ExecContext(ctx, `UPDATE agent_commands SET state=?,
		dispatched_at=CASE WHEN ?='dispatched' THEN ? ELSE dispatched_at END,
		accepted_at=CASE WHEN ?='accepted' THEN ? ELSE accepted_at END,
		started_at=CASE WHEN ?='running' THEN ? ELSE started_at END,
		finished_at=CASE WHEN ? IN ('succeeded','failed','expired') THEN ? ELSE finished_at END,
		attempt_count=attempt_count+CASE WHEN ?='dispatched' THEN 1 ELSE 0 END,
		result_code=CASE WHEN ? IN ('succeeded','failed') THEN ? ELSE result_code END,
		result_json=CASE WHEN ? IN ('succeeded','failed') THEN ? ELSE result_json END
		WHERE command_id=? AND state=?`,
		to, to, timestamp, to, timestamp, to, timestamp, to, timestamp, to, to, resultCode, to, string(resultJSON), commandID, from)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("command transition lost concurrent update")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_command_events(command_id,actor_id,session_id,from_state,to_state,created_at,reason_code) VALUES(?,?,?,?,?,?,?)`,
		commandID, actor.UserID, actor.SessionID, from, to, timestamp, reasonCode); err != nil {
		return err
	}
	return tx.Commit()
}

func allowedTransition(from, to State) bool {
	switch from {
	case Queued:
		return to == Dispatched || to == Failed || to == Expired
	case Dispatched:
		return to == Accepted || to == Failed || to == Expired
	case Accepted:
		return to == Running || to == Failed || to == Expired
	case Running:
		return to == Succeeded || to == Failed
	default:
		return false
	}
}

func (s *Store) QueuedFor(ctx context.Context, agentID string, now time.Time) ([]controlproto.Command, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT control_version,message_type,command_id,sequence,agent_id,action,issued_at,expires_at,payload,signature
		FROM agent_commands WHERE agent_id=? AND state IN ('queued','dispatched') AND expires_at>? ORDER BY sequence`, agentID, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	commands := make([]controlproto.Command, 0)
	for rows.Next() {
		var command controlproto.Command
		var payload string
		if err := rows.Scan(&command.ControlVersion, &command.Type, &command.CommandID, &command.Sequence, &command.AgentID, &command.Action,
			&command.IssuedAt, &command.ExpiresAt, &payload, &command.Signature); err != nil {
			return nil, err
		}
		command.Payload = json.RawMessage(payload)
		commands = append(commands, command)
	}
	return commands, rows.Err()
}

func (s *Store) History(ctx context.Context, agentID string, limit int) ([]HistoryItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT control_version,message_type,command_id,sequence,agent_id,action,issued_at,expires_at,payload,signature,state,
		dispatched_at,accepted_at,started_at,finished_at,attempt_count,result_code,result_json
		FROM agent_commands WHERE agent_id=? ORDER BY sequence DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]HistoryItem, 0)
	for rows.Next() {
		var item HistoryItem
		var payload, resultJSON string
		if err := rows.Scan(&item.Command.ControlVersion, &item.Command.Type, &item.Command.CommandID, &item.Command.Sequence, &item.Command.AgentID,
			&item.Command.Action, &item.Command.IssuedAt, &item.Command.ExpiresAt, &payload, &item.Command.Signature, &item.State,
			&item.DispatchedAt, &item.AcceptedAt, &item.StartedAt, &item.FinishedAt, &item.AttemptCount, &item.ResultCode, &resultJSON); err != nil {
			return nil, err
		}
		item.Command.Payload = json.RawMessage(payload)
		item.Result = json.RawMessage(resultJSON)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Expire(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT command_id FROM agent_commands WHERE state IN ('queued','dispatched','accepted') AND expires_at<=? ORDER BY agent_id,sequence`, now.Unix())
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := s.transitionAt(ctx, id, Expired, auth.Actor{}, "ttl_expired", "", nil, now.UTC().Truncate(time.Second)); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (s *Store) ApplyAgentResult(ctx context.Context, agentID string, result controlproto.CommandResult) error {
	if result.ControlVersion != controlproto.Version || result.Type != "command_result" || result.CommandID == "" ||
		len(result.Code) > 128 || len(result.Data) > 16*1024 || (len(result.Data) > 0 && !json.Valid(result.Data)) {
		return errors.New("invalid Agent command result")
	}
	if result.State != string(Accepted) && result.State != string(Running) && result.State != string(Succeeded) && result.State != string(Failed) {
		return errors.New("invalid Agent command result state")
	}
	if len(result.Data) == 0 {
		result.Data = json.RawMessage(`{}`)
	}
	var storedAgentID, role string
	var sequence uint64
	var state State
	var actor auth.Actor
	var storedCode, storedJSON string
	err := s.db.QueryRowContext(ctx, `SELECT c.agent_id,c.sequence,c.state,c.created_by_user_id,c.created_session_id,u.username,u.role,c.result_code,c.result_json
		FROM agent_commands c JOIN users u ON u.id=c.created_by_user_id WHERE c.command_id=?`, result.CommandID).
		Scan(&storedAgentID, &sequence, &state, &actor.UserID, &actor.SessionID, &actor.Username, &role, &storedCode, &storedJSON)
	if err != nil {
		return err
	}
	actor.Role = auth.Role(role)
	if storedAgentID != agentID || sequence != result.Sequence {
		return errors.New("Agent command result target mismatch")
	}
	if state == Succeeded || state == Failed {
		if string(state) == result.State && storedCode == result.Code && jsonEqual([]byte(storedJSON), result.Data) {
			return nil
		}
		return errors.New("terminal Agent command result changed")
	}
	if state == Queued || state == Expired {
		return errors.New("Agent result received for undelivered command")
	}
	advance := func(to State, reason string, code string, data []byte) error {
		if err := s.Transition(ctx, result.CommandID, to, actor, reason, code, data); err != nil {
			return err
		}
		state = to
		return nil
	}
	if state == Dispatched && (result.State == string(Accepted) || result.State == string(Running) || result.State == string(Succeeded) || result.State == string(Failed)) {
		if err := advance(Accepted, "agent_accepted", "", nil); err != nil {
			return err
		}
	}
	if state == Accepted && (result.State == string(Running) || result.State == string(Succeeded) || result.State == string(Failed)) {
		if err := advance(Running, "agent_running", "", nil); err != nil {
			return err
		}
	}
	if result.State == string(Accepted) || result.State == string(Running) {
		return nil
	}
	return advance(State(result.State), "agent_terminal", result.Code, result.Data)
}

func jsonEqual(left, right []byte) bool {
	return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
}

func commandUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	hexValue := hex.EncodeToString(value)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:]), nil
}
