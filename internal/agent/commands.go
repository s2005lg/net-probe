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
	"sync/atomic"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
)

const commandStateFile = "commands.json"

type CommandOutcome struct {
	Code         string
	Data         json.RawMessage
	Failed       bool
	Pending      bool
	AfterPersist func()
}

type CommandHandler func(context.Context, json.RawMessage, string) CommandOutcome

type commandEnvelopeContextKey struct{}

func commandEnvelopeFromContext(ctx context.Context) (controlproto.Command, bool) {
	command, ok := ctx.Value(commandEnvelopeContextKey{}).(controlproto.Command)
	return command, ok
}

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
	executeMu sync.Mutex
	mu        sync.Mutex
	stateDir  string
	agentID   string
	publicKey ed25519.PublicKey
	handlers  map[controlproto.Action]CommandHandler
	state     executorState
	currentID atomic.Value
	completed atomic.Uint64
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
	executor.currentID.Store("")
	executor.completed.Store(executor.state.HighestCompleted)
	return executor, nil
}

func (e *CommandExecutor) Execute(ctx context.Context, command controlproto.Command) controlproto.CommandResult {
	result := func(code string) controlproto.CommandResult { return failedCommandResult(command, code) }
	if command.ControlVersion != controlproto.Version || command.Type != "command" || controlproto.TTL(command.Action) <= 0 || !json.Valid(command.Payload) {
		return result("invalid_command")
	}
	if err := controlproto.VerifyCommand(e.publicKey, command); err != nil {
		return result("invalid_signature")
	}
	if command.AgentID != e.agentID {
		return result("wrong_agent")
	}
	e.executeMu.Lock()
	defer e.executeMu.Unlock()
	e.mu.Lock()
	digest := commandDigest(command)
	for index := range e.state.Records {
		record := &e.state.Records[index]
		if record.CommandID != command.CommandID {
			continue
		}
		if record.Sequence != command.Sequence || record.Digest != digest {
			e.mu.Unlock()
			return result("duplicate_mismatch")
		}
		if terminalCommandState(record.State) {
			prior := cloneCommandResult(record.Result)
			e.mu.Unlock()
			return prior
		}
		return e.resumeLocked(ctx, command, index)
	}
	now := e.now()
	if command.IssuedAt > now.Add(time.Minute).Unix() {
		e.mu.Unlock()
		return result("issued_in_future")
	}
	if command.ExpiresAt <= now.Unix() || command.ExpiresAt <= command.IssuedAt {
		e.mu.Unlock()
		return result("expired")
	}
	if command.Sequence <= e.state.HighestSeen {
		e.mu.Unlock()
		return result("stale_sequence")
	}
	if e.handlers[command.Action] == nil {
		e.mu.Unlock()
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
		e.mu.Unlock()
		return result("state_write_failed")
	}
	for index := range e.state.Records {
		if e.state.Records[index].CommandID == command.CommandID {
			return e.resumeLocked(ctx, command, index)
		}
	}
	e.mu.Unlock()
	return result("state_write_failed")
}

// resumeLocked starts with e.mu held and releases it while the bounded action
// runs, keeping presence getters responsive for heartbeats and reconnects.
func (e *CommandExecutor) resumeLocked(ctx context.Context, command controlproto.Command, index int) controlproto.CommandResult {
	handler := e.handlers[command.Action]
	if handler == nil {
		e.mu.Unlock()
		return failedCommandResult(command, "unsupported_action")
	}
	record := &e.state.Records[index]
	previousState, previousResult := record.State, cloneCommandResult(record.Result)
	record.State = "running"
	record.Result.State = "running"
	record.Result.Code = "running"
	if err := e.persist(); err != nil {
		record.State, record.Result = previousState, previousResult
		e.mu.Unlock()
		return failedCommandResult(command, "state_write_failed")
	}
	e.currentID.Store(command.CommandID)
	e.mu.Unlock()
	outcome := callCommandHandler(ctx, handler, command)
	e.currentID.Store("")
	if outcome.Pending && outcome.Failed {
		outcome = CommandOutcome{Code: "invalid_handler_result", Data: json.RawMessage(`{}`), Failed: true}
	}
	state := "succeeded"
	if outcome.Failed {
		state = "failed"
	}
	if outcome.Pending {
		state = "running"
	}
	if outcome.Code == "" {
		outcome.Code = state
	}
	if len(outcome.Code) > 128 || len(outcome.Data) > 16*1024 || (len(outcome.Data) > 0 && !json.Valid(outcome.Data)) {
		state, outcome.Code, outcome.Data = "failed", "invalid_handler_result", json.RawMessage(`{}`)
		outcome.Pending = false
	}
	if len(outcome.Data) == 0 {
		outcome.Data = json.RawMessage(`{}`)
	}
	e.mu.Lock()
	record = &e.state.Records[index]
	record.State = state
	record.Result = controlproto.CommandResult{
		ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID,
		Sequence: command.Sequence, State: state, Code: outcome.Code, Data: append(json.RawMessage(nil), outcome.Data...),
	}
	if !outcome.Pending && command.Sequence > e.state.HighestCompleted {
		e.state.HighestCompleted = command.Sequence
	}
	if err := e.persist(); err != nil {
		e.mu.Unlock()
		return failedCommandResult(command, "state_write_failed")
	}
	e.completed.Store(e.state.HighestCompleted)
	result := cloneCommandResult(record.Result)
	e.mu.Unlock()
	if outcome.AfterPersist != nil && !outcome.Pending {
		func() {
			defer func() { _ = recover() }()
			outcome.AfterPersist()
		}()
	}
	return result
}

func callCommandHandler(ctx context.Context, handler CommandHandler, command controlproto.Command) (outcome CommandOutcome) {
	defer func() {
		if recover() != nil {
			outcome = CommandOutcome{Code: "handler_failed", Data: json.RawMessage(`{}`), Failed: true}
		}
	}()
	command.Payload = append(json.RawMessage(nil), command.Payload...)
	return handler(context.WithValue(ctx, commandEnvelopeContextKey{}, command), command.Payload, command.CommandID)
}

func (e *CommandExecutor) HighestCompleted() uint64 {
	return e.completed.Load()
}

func (e *CommandExecutor) CurrentCommandID() string {
	value, _ := e.currentID.Load().(string)
	return value
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

func failedCommandResult(command controlproto.Command, code string) controlproto.CommandResult {
	return controlproto.CommandResult{
		ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID,
		Sequence: command.Sequence, State: "failed", Code: code, Data: json.RawMessage(`{}`),
	}
}
