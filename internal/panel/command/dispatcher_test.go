package command

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	panelcontrol "github.com/s2005lg/net-probe/internal/panel/control"
)

type dispatcherSession struct {
	id       string
	outbound chan []byte
}

func (s *dispatcherSession) SessionID() string       { return s.id }
func (s *dispatcherSession) Outbound() chan<- []byte { return s.outbound }
func (s *dispatcherSession) Close(string)            {}

func TestDispatcherLeavesOfflineQueuedAndMarksOnlyAfterHubEnqueue(t *testing.T) {
	store, public, d := openCommandStore(t)
	command := createCommand(t, store, controlproto.CollectNow, `{}`)
	hub := panelcontrol.NewHub(1000, 32)
	dispatcher := NewDispatcher(store, hub)
	if err := dispatcher.DispatchAgent(context.Background(), storeAgentID); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM agent_commands WHERE command_id=?`, command.CommandID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(Queued) {
		t.Fatalf("offline state=%q", state)
	}
	session := &dispatcherSession{id: "session-1", outbound: make(chan []byte, 32)}
	if err := hub.Register(storeAgentID, session); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchAgent(context.Background(), storeAgentID); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-session.outbound:
		var sent controlproto.Command
		if err := controlproto.StrictDecode(body, &sent); err != nil {
			t.Fatal(err)
		}
		if sent.CommandID != command.CommandID || controlproto.VerifyCommand(public, sent) != nil {
			t.Fatalf("sent=%+v", sent)
		}
	case <-time.After(time.Second):
		t.Fatal("command not enqueued")
	}
	if err := d.QueryRow(`SELECT state FROM agent_commands WHERE command_id=?`, command.CommandID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(Dispatched) {
		t.Fatalf("online state=%q", state)
	}
}

func TestDispatcherRechecksCreatorRoleBeforeEveryDelivery(t *testing.T) {
	store, _, d := openCommandStore(t)
	command := createCommand(t, store, controlproto.SelfCheck, `{"checks":["config"]}`)
	if _, err := d.Exec(`UPDATE users SET role='viewer' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	hub := panelcontrol.NewHub(1000, 32)
	session := &dispatcherSession{id: "session-1", outbound: make(chan []byte, 32)}
	if err := hub.Register(storeAgentID, session); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher(store, hub)
	if err := dispatcher.DispatchAgent(context.Background(), storeAgentID); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-session.outbound:
		var value any
		_ = json.Unmarshal(body, &value)
		t.Fatalf("revoked actor command delivered: %v", value)
	default:
	}
	var state, code string
	if err := d.QueryRow(`SELECT state,result_code FROM agent_commands WHERE command_id=?`, command.CommandID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != string(Failed) || code != "authorization_revoked" {
		t.Fatalf("state=%q code=%q", state, code)
	}
}

func TestDispatcherTreatsBackpressureAsQueuedWork(t *testing.T) {
	store, _, d := openCommandStore(t)
	command := createCommand(t, store, controlproto.CollectNow, `{}`)
	hub := panelcontrol.NewHub(1000, 1)
	session := &dispatcherSession{id: "session-1", outbound: make(chan []byte, 1)}
	if err := hub.Register(storeAgentID, session); err != nil {
		t.Fatal(err)
	}
	session.outbound <- []byte("occupied")
	dispatcher := NewDispatcher(store, hub)
	if err := dispatcher.DispatchAgent(context.Background(), storeAgentID); err != nil && !errors.Is(err, panelcontrol.ErrBackpressure) {
		t.Fatal(err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM agent_commands WHERE command_id=?`, command.CommandID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(Queued) {
		t.Fatalf("state=%q", state)
	}
}
