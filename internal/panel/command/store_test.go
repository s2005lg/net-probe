package command

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
	paneldb "github.com/s2005lg/net-probe/internal/panel/db"
)

const storeAgentID = "123e4567-e89b-42d3-a456-426614174030"

func openCommandStore(t *testing.T) (*Store, ed25519.PublicKey, *sql.DB) {
	t.Helper()
	d, err := paneldb.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := paneldb.Migrate(d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO users(id,username,password_hash,created_at,role) VALUES(1,'operator','x',1,'operator')`); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(d, private)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return store, public, d
}

func createCommand(t *testing.T, store *Store, action controlproto.Action, payload string) controlproto.Command {
	t.Helper()
	command, err := store.Create(context.Background(), auth.Actor{UserID: 1, Role: auth.Operator}, storeAgentID, action, []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func TestCreateAssignsMonotonicSignedSequence(t *testing.T) {
	store, public, _ := openCommandStore(t)
	first := createCommand(t, store, controlproto.CollectNow, `{}`)
	second := createCommand(t, store, controlproto.SelfCheck, `{"checks":["config"]}`)
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequence=%d,%d", first.Sequence, second.Sequence)
	}
	if err := controlproto.VerifyCommand(public, second); err != nil {
		t.Fatal(err)
	}
	if first.ExpiresAt-first.IssuedAt != int64(controlproto.TTL(controlproto.CollectNow)/time.Second) {
		t.Fatalf("ttl=%d", first.ExpiresAt-first.IssuedAt)
	}
}

func TestCreateConcurrentSequencesAreUniqueAndContiguous(t *testing.T) {
	store, public, _ := openCommandStore(t)
	commands := make([]controlproto.Command, 20)
	errs := make([]error, 20)
	var wg sync.WaitGroup
	for i := range commands {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			commands[index], errs[index] = store.Create(context.Background(), auth.Actor{UserID: 1, Role: auth.Operator}, storeAgentID, controlproto.CollectNow, []byte(`{}`))
		}(i)
	}
	wg.Wait()
	sequences := make([]int, 0, len(commands))
	for index, command := range commands {
		if errs[index] != nil {
			t.Fatalf("create %d: %v", index, errs[index])
		}
		if err := controlproto.VerifyCommand(public, command); err != nil {
			t.Fatalf("verify %d: %v", index, err)
		}
		sequences = append(sequences, int(command.Sequence))
	}
	sort.Ints(sequences)
	for index, sequence := range sequences {
		if sequence != index+1 {
			t.Fatalf("sequences=%v", sequences)
		}
	}
}

func TestCreatePersistsQueuedCommandAndAppendOnlyEvent(t *testing.T) {
	store, _, d := openCommandStore(t)
	command := createCommand(t, store, controlproto.ReloadConfig, `{}`)
	var state string
	if err := d.QueryRow(`SELECT state FROM agent_commands WHERE command_id=?`, command.CommandID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "queued" {
		t.Fatalf("state=%q", state)
	}
	var events int
	if err := d.QueryRow(`SELECT count(*) FROM agent_command_events WHERE command_id=? AND from_state='' AND to_state='queued'`, command.CommandID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("events=%d", events)
	}
	if _, err := d.Exec(`UPDATE agent_command_events SET reason_code='tampered' WHERE command_id=?`, command.CommandID); err == nil {
		t.Fatal("append-only event was updated")
	}
}

func TestTransitionEnforcesStateMachineAndResultBound(t *testing.T) {
	store, _, d := openCommandStore(t)
	command := createCommand(t, store, controlproto.CollectNow, `{}`)
	actor := auth.Actor{UserID: 1, Role: auth.Operator}
	if err := store.Transition(context.Background(), command.CommandID, Running, actor, "invalid_skip", "", nil); err == nil {
		t.Fatal("queued command skipped directly to running")
	}
	if err := store.Transition(context.Background(), command.CommandID, Dispatched, actor, "hub_enqueued", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(context.Background(), command.CommandID, Accepted, actor, "agent_accepted", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(context.Background(), command.CommandID, Running, actor, "agent_running", "", nil); err != nil {
		t.Fatal(err)
	}
	oversized := make([]byte, 16*1024+1)
	if err := store.Transition(context.Background(), command.CommandID, Succeeded, actor, "done", "ok", oversized); err == nil {
		t.Fatal("accepted oversized result")
	}
	if err := store.Transition(context.Background(), command.CommandID, Succeeded, actor, "done", "ok", []byte(`{"status":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	var state, resultCode string
	if err := d.QueryRow(`SELECT state,result_code FROM agent_commands WHERE command_id=?`, command.CommandID).Scan(&state, &resultCode); err != nil {
		t.Fatal(err)
	}
	if state != string(Succeeded) || resultCode != "ok" {
		t.Fatalf("state=%q result=%q", state, resultCode)
	}
}

func TestQueuedForReturnsOrderedUnexpiredCommandsAndExpireTransitions(t *testing.T) {
	store, _, _ := openCommandStore(t)
	first := createCommand(t, store, controlproto.CollectNow, `{}`)
	second := createCommand(t, store, controlproto.SelfCheck, `{"checks":["config"]}`)
	actor := auth.Actor{UserID: 1, Role: auth.Operator}
	if err := store.Transition(context.Background(), first.CommandID, Dispatched, actor, "hub_enqueued", "", nil); err != nil {
		t.Fatal(err)
	}
	queued, err := store.QueuedFor(context.Background(), storeAgentID, time.Unix(first.IssuedAt+1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 || queued[0].Sequence != 1 || queued[1].Sequence != 2 {
		t.Fatalf("queued=%+v", queued)
	}
	count, err := store.Expire(context.Background(), time.Unix(second.ExpiresAt+1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expired=%d", count)
	}
	queued, err = store.QueuedFor(context.Background(), storeAgentID, time.Unix(second.ExpiresAt+1, 0))
	if err != nil || len(queued) != 0 {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
}

func TestApplyAgentResultAdvancesLifecycleAndIsIdempotent(t *testing.T) {
	store, _, d := openCommandStore(t)
	command := createCommand(t, store, controlproto.CollectNow, `{}`)
	actor := auth.Actor{UserID: 1, Role: auth.Operator}
	if err := store.Transition(context.Background(), command.CommandID, Dispatched, actor, "hub_enqueued", "", nil); err != nil {
		t.Fatal(err)
	}
	result := controlproto.CommandResult{
		ControlVersion: controlproto.Version, Type: "command_result", CommandID: command.CommandID,
		Sequence: command.Sequence, State: "succeeded", Code: "report_acknowledged", Data: json.RawMessage(`{"status":"ok"}`),
	}
	if err := store.ApplyAgentResult(context.Background(), storeAgentID, result); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyAgentResult(context.Background(), storeAgentID, result); err != nil {
		t.Fatalf("duplicate result: %v", err)
	}
	var state, code string
	if err := d.QueryRow(`SELECT state,result_code FROM agent_commands WHERE command_id=?`, command.CommandID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != string(Succeeded) || code != "report_acknowledged" {
		t.Fatalf("state=%q code=%q", state, code)
	}
	var accepted, running, succeeded int
	if err := d.QueryRow(`SELECT
		SUM(CASE WHEN to_state='accepted' THEN 1 ELSE 0 END),
		SUM(CASE WHEN to_state='running' THEN 1 ELSE 0 END),
		SUM(CASE WHEN to_state='succeeded' THEN 1 ELSE 0 END)
		FROM agent_command_events WHERE command_id=?`, command.CommandID).Scan(&accepted, &running, &succeeded); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 || running != 1 || succeeded != 1 {
		t.Fatalf("events accepted=%d running=%d succeeded=%d", accepted, running, succeeded)
	}
}
