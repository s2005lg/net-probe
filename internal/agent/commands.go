package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
)

const commandStateFile = "commands.json"

type CommandOutcome struct {
	Code   string
	Data   json.RawMessage
	Failed bool
}

type CommandHandler func(context.Context, json.RawMessage, string) CommandOutcome

type commandRecord struct {
	CommandID string                     `json:"command_id"`
	Sequence  uint64                     `json:"sequence"`
	Digest    string                     `json:"digest"`
	State     string                     `json:"state"`
	Result    controlproto.CommandResult `json:"result"`
}

type executorState struct {
	Version          string          `json:"version"`
	HighestSeen      uint64          `json:"highest_seen"`
	HighestCompleted uint64          `json:"highest_completed"`
	Records          []commandRecord `json:"records"`
}

type CommandExecutor struct {
	mu        sync.Mutex
	stateDir  string
	agentID   string
	publicKey ed25519.PublicKey
	handlers  map[controlproto.Action]CommandHandler
	state     executorState
	currentID string
	now       func() time.Time
}

func OpenCommandExecutor(stateDir, agentID string, publicKey ed25519.PublicKey, handlers map[controlproto.Action]CommandHandler) (*CommandExecutor, error) {
	if stateDir == "" || agentID == "" || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("command executor requires state directory, Agent ID, and verification key")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return nil, err
	}
	executor := &CommandExecutor{
		stateDir: stateDir, agentID: agentID, publicKey: ed25519.PublicKey(bytes.Clone(publicKey)),
		handlers: make(map[controlproto.Action]CommandHandler, len(handlers)), now: time.Now,
		state: executorState{Version: "1", Records: make([]commandRecord, 0)},
	}
	for action, handler := range handlers {
		if controlproto.TTL(action) <= 0 || handler == nil {
			return nil, errors.New("command executor handler map is invalid")
		}
		executor.handlers[action] = handler
	}
	if err := executor.load(); err != nil {
		return nil, err
	}
	return executor, nil
}

func (e *CommandExecutor) Execute(ctx context.Context, command controlproto.Command) controlproto.CommandResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := func(code string) controlproto.CommandResult {
		return controlproto.CommandResult{
			ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID,
			Sequence: command.Sequence, State: "failed", Code: code, Data: json.RawMessage(`{}`),
		}
	}
	if command.ControlVersion != controlproto.Version || command.Type != "command" || controlproto.TTL(command.Action) <= 0 || !json.Valid(command.Payload) {
		return result("invalid_command")
	}
	if err := controlproto.VerifyCommand(e.publicKey, command); err != nil {
		return result("invalid_signature")
	}
	if command.AgentID != e.agentID {
		return result("wrong_agent")
	}
	now := e.now()
	if command.IssuedAt > now.Add(time.Minute).Unix() {
		return result("issued_in_future")
	}
	if command.ExpiresAt <= now.Unix() || command.ExpiresAt <= command.IssuedAt {
		return result("expired")
	}
	digest := commandDigest(command)
	for index := range e.state.Records {
		record := &e.state.Records[index]
		if record.CommandID != command.CommandID {
			continue
		}
		if record.Sequence != command.Sequence || record.Digest != digest {
			return result("duplicate_mismatch")
		}
		if terminalCommandState(record.State) {
			return cloneCommandResult(record.Result)
		}
		return e.resume(ctx, command, record)
	}
	if command.Sequence <= e.state.HighestSeen {
		return result("stale_sequence")
	}
	if e.handlers[command.Action] == nil {
		return result("unsupported_action")
	}
	record := commandRecord{
		CommandID: command.CommandID, Sequence: command.Sequence, Digest: digest, State: "accepted",
		Result: controlproto.CommandResult{ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID, Sequence: command.Sequence, State: "accepted", Code: "accepted", Data: json.RawMessage(`{}`)},
	}
	e.state.HighestSeen = command.Sequence
	e.state.Records = append(e.state.Records, record)
	e.trimRecords()
	if err := e.persist(); err != nil {
		return result("state_write_failed")
	}
	for index := range e.state.Records {
		if e.state.Records[index].CommandID == command.CommandID {
			return e.resume(ctx, command, &e.state.Records[index])
		}
	}
	return result("state_write_failed")
}

func (e *CommandExecutor) resume(ctx context.Context, command controlproto.Command, record *commandRecord) controlproto.CommandResult {
	handler := e.handlers[command.Action]
	if handler == nil {
		return controlproto.CommandResult{
			ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID,
			Sequence: command.Sequence, State: "failed", Code: "unsupported_action", Data: json.RawMessage(`{}`),
		}
	}
	record.State = "running"
	record.Result.State = "running"
	record.Result.Code = "running"
	e.currentID = command.CommandID
	if err := e.persist(); err != nil {
		e.currentID = ""
		return controlproto.CommandResult{
			ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID,
			Sequence: command.Sequence, State: "failed", Code: "state_write_failed", Data: json.RawMessage(`{}`),
		}
	}
	outcome := callCommandHandler(ctx, handler, command.Payload, command.CommandID)
	e.currentID = ""
	state := "succeeded"
	if outcome.Failed {
		state = "failed"
	}
	if outcome.Code == "" {
		outcome.Code = state
	}
	if len(outcome.Code) > 128 || len(outcome.Data) > 16*1024 || (len(outcome.Data) > 0 && !json.Valid(outcome.Data)) {
		state, outcome.Code, outcome.Data = "failed", "invalid_handler_result", json.RawMessage(`{}`)
	}
	if len(outcome.Data) == 0 {
		outcome.Data = json.RawMessage(`{}`)
	}
	record.State = state
	record.Result = controlproto.CommandResult{
		ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID,
		Sequence: command.Sequence, State: state, Code: outcome.Code, Data: append(json.RawMessage(nil), outcome.Data...),
	}
	if command.Sequence > e.state.HighestCompleted {
		e.state.HighestCompleted = command.Sequence
	}
	if err := e.persist(); err != nil {
		return controlproto.CommandResult{
			ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID,
			Sequence: command.Sequence, State: "failed", Code: "state_write_failed", Data: json.RawMessage(`{}`),
		}
	}
	return cloneCommandResult(record.Result)
}

func callCommandHandler(ctx context.Context, handler CommandHandler, payload json.RawMessage, commandID string) (outcome CommandOutcome) {
	defer func() {
		if recover() != nil {
			outcome = CommandOutcome{Code: "handler_failed", Data: json.RawMessage(`{}`), Failed: true}
		}
	}()
	return handler(ctx, append(json.RawMessage(nil), payload...), commandID)
}

func (e *CommandExecutor) HighestCompleted() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.HighestCompleted
}

func (e *CommandExecutor) CurrentCommandID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentID
}

func (e *CommandExecutor) load() error {
	path := filepath.Join(e.stateDir, commandStateFile)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var state executorState
	if err := decoder.Decode(&state); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("command state contains trailing JSON")
	}
	if state.Version != "1" || len(state.Records) > 256 {
		return errors.New("command state is invalid")
	}
	e.state = state
	if e.state.Records == nil {
		e.state.Records = make([]commandRecord, 0)
	}
	return nil
}

func (e *CommandExecutor) persist() error {
	body, err := json.Marshal(e.state)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(e.stateDir, commandStateFile), body); err != nil {
		return err
	}
	return syncDirectory(e.stateDir)
}

func (e *CommandExecutor) trimRecords() {
	if len(e.state.Records) <= 256 {
		return
	}
	sort.Slice(e.state.Records, func(i, j int) bool { return e.state.Records[i].Sequence < e.state.Records[j].Sequence })
	e.state.Records = append([]commandRecord(nil), e.state.Records[len(e.state.Records)-256:]...)
}

func commandDigest(command controlproto.Command) string {
	digest := sha256.Sum256(controlproto.SigningBytes(command))
	return hex.EncodeToString(digest[:])
}

func terminalCommandState(state string) bool {
	return state == "succeeded" || state == "failed"
}

func cloneCommandResult(result controlproto.CommandResult) controlproto.CommandResult {
	result.Data = append(json.RawMessage(nil), result.Data...)
	return result
}
