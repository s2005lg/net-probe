package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
)

const executorAgentID = "123e4567-e89b-42d3-a456-426614174040"

func signedExecutorCommand(t *testing.T, private ed25519.PrivateKey, sequence uint64, action controlproto.Action, payload string) controlproto.Command {
	t.Helper()
	now := time.Now().Unix()
	command := controlproto.Command{
		ControlVersion: controlproto.Version, Type: "command", CommandID: "123e4567-e89b-42d3-a456-426614174041",
		Sequence: sequence, AgentID: executorAgentID, Action: action, IssuedAt: now, ExpiresAt: now + 300, Payload: json.RawMessage(payload),
	}
	if err := controlproto.SignCommand(private, &command); err != nil {
		t.Fatal(err)
	}
	return command
}

func TestExecutorReturnsDurablePriorResultAfterRestart(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	var calls atomic.Int32
	handlers := map[controlproto.Action]CommandHandler{
		controlproto.CollectNow: func(context.Context, json.RawMessage, string) CommandOutcome {
			calls.Add(1)
			return CommandOutcome{Code: "report_acknowledged", Data: json.RawMessage(`{"status":"ok"}`)}
		},
	}
	firstExecutor, err := OpenCommandExecutor(stateDir, executorAgentID, public, handlers)
	if err != nil {
		t.Fatal(err)
	}
	command := signedExecutorCommand(t, private, 1, controlproto.CollectNow, `{}`)
	want := firstExecutor.Execute(context.Background(), command)
	secondExecutor, err := OpenCommandExecutor(stateDir, executorAgentID, public, handlers)
	if err != nil {
		t.Fatal(err)
	}
	got := secondExecutor.Execute(context.Background(), command)
	if got.Code != want.Code || string(got.Data) != string(want.Data) || calls.Load() != 1 {
		t.Fatalf("got=%+v want=%+v calls=%d", got, want, calls.Load())
	}
	info, err := os.Stat(filepath.Join(stateDir, commandStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode=%o", info.Mode().Perm())
	}
}

func TestExecutorRejectsForgeryTargetFutureAndChangedDuplicate(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	executor, err := OpenCommandExecutor(t.TempDir(), executorAgentID, public, map[controlproto.Action]CommandHandler{
		controlproto.CollectNow: func(context.Context, json.RawMessage, string) CommandOutcome {
			calls.Add(1)
			return CommandOutcome{Code: "ok"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := signedExecutorCommand(t, private, 1, controlproto.CollectNow, `{}`)
	if result := executor.Execute(context.Background(), valid); result.State != "succeeded" {
		t.Fatalf("valid=%+v", result)
	}

	changed := valid
	changed.Payload = json.RawMessage(`{"changed":true}`)
	if err := controlproto.SignCommand(private, &changed); err != nil {
		t.Fatal(err)
	}
	if result := executor.Execute(context.Background(), changed); result.Code != "duplicate_mismatch" {
		t.Fatalf("changed duplicate=%+v", result)
	}
	wrongTarget := signedExecutorCommand(t, private, 2, controlproto.CollectNow, `{}`)
	wrongTarget.AgentID = "123e4567-e89b-42d3-a456-426614174099"
	if err := controlproto.SignCommand(private, &wrongTarget); err != nil {
		t.Fatal(err)
	}
	if result := executor.Execute(context.Background(), wrongTarget); result.Code != "wrong_agent" {
		t.Fatalf("wrong target=%+v", result)
	}
	future := signedExecutorCommand(t, private, 2, controlproto.CollectNow, `{}`)
	future.IssuedAt = time.Now().Add(61 * time.Second).Unix()
	future.ExpiresAt = future.IssuedAt + 300
	if err := controlproto.SignCommand(private, &future); err != nil {
		t.Fatal(err)
	}
	if result := executor.Execute(context.Background(), future); result.Code != "issued_in_future" {
		t.Fatalf("future=%+v", result)
	}
	forged := signedExecutorCommand(t, private, 2, controlproto.CollectNow, `{}`)
	forged.Signature = "forged"
	if result := executor.Execute(context.Background(), forged); result.Code != "invalid_signature" {
		t.Fatalf("forged=%+v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls=%d", calls.Load())
	}
}

func TestExecutorPresenceGettersDoNotBlockDuringCommand(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	executor, err := OpenCommandExecutor(t.TempDir(), executorAgentID, public, map[controlproto.Action]CommandHandler{
		controlproto.CollectNow: func(context.Context, json.RawMessage, string) CommandOutcome {
			close(started)
			<-release
			return CommandOutcome{Code: "ok"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := signedExecutorCommand(t, private, 1, controlproto.CollectNow, `{}`)
	done := make(chan controlproto.CommandResult, 1)
	go func() { done <- executor.Execute(context.Background(), command) }()
	<-started
	gettersDone := make(chan struct{})
	go func() {
		defer close(gettersDone)
		if executor.CurrentCommandID() != command.CommandID {
			t.Errorf("current command=%q", executor.CurrentCommandID())
		}
		_ = executor.HighestCompleted()
	}()
	select {
	case <-gettersDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("presence getters blocked behind command handler")
	}
	close(release)
	if result := <-done; result.State != "succeeded" {
		t.Fatalf("result=%+v", result)
	}
}

func TestExecutorPersistsRunningOutcomeAndResumesAfterRestart(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	var finished atomic.Bool
	handler := func(context.Context, json.RawMessage, string) CommandOutcome {
		if !finished.Load() {
			return CommandOutcome{Code: "upgrade_staged", Data: json.RawMessage(`{}`), Pending: true}
		}
		return CommandOutcome{Code: "upgrade_applied", Data: json.RawMessage(`{}`)}
	}
	first, err := OpenCommandExecutor(stateDir, executorAgentID, public, map[controlproto.Action]CommandHandler{controlproto.Upgrade: handler})
	if err != nil {
		t.Fatal(err)
	}
	command := signedExecutorCommand(t, private, 1, controlproto.Upgrade, `{}`)
	if result := first.Execute(context.Background(), command); result.State != "running" || result.Code != "upgrade_staged" || first.HighestCompleted() != 0 {
		t.Fatalf("staged result=%+v highest=%d", result, first.HighestCompleted())
	}
	finished.Store(true)
	second, err := OpenCommandExecutor(stateDir, executorAgentID, public, map[controlproto.Action]CommandHandler{controlproto.Upgrade: handler})
	if err != nil {
		t.Fatal(err)
	}
	if result := second.Execute(context.Background(), command); result.State != "succeeded" || result.Code != "upgrade_applied" || second.HighestCompleted() != 1 {
		t.Fatalf("resumed result=%+v highest=%d", result, second.HighestCompleted())
	}
}
