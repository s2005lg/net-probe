package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/s2005lg/net-probe/internal/sink"
)

type switchSink struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (s *switchSink) Send(context.Context, []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *switchSink) setError(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *switchSink) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestReporterRetriesDestinationsIndependently(t *testing.T) {
	q, err := NewOutbox(t.TempDir(), Limits{MaxItems: 120, MaxBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	panel := &switchSink{}
	webhook := &switchSink{err: errors.New("offline")}
	r, err := NewReporter(q, []Destination{
		{ID: "panel", Sink: sink.Sink(panel)},
		{ID: "webhook:0", Sink: sink.Sink(webhook)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Send(context.Background(), []byte(`{"node_id":"n1"}`)); err == nil {
		t.Fatal("Send succeeded while a destination remained pending")
	}
	items, err := q.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Pending) != 1 || items[0].Pending[0] != "webhook:0" {
		t.Fatalf("items=%+v", items)
	}
	if panel.Calls() != 1 || webhook.Calls() != 1 {
		t.Fatalf("calls panel=%d webhook=%d", panel.Calls(), webhook.Calls())
	}

	webhook.setError(nil)
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err = q.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || panel.Calls() != 1 || webhook.Calls() != 2 {
		t.Fatalf("items=%+v calls panel=%d webhook=%d", items, panel.Calls(), webhook.Calls())
	}
}

func TestReporterRejectsDuplicateDestinationIDs(t *testing.T) {
	q, err := NewOutbox(t.TempDir(), Limits{MaxItems: 1, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewReporter(q, []Destination{{ID: "panel", Sink: &switchSink{}}, {ID: "panel", Sink: &switchSink{}}})
	if err == nil {
		t.Fatal("accepted duplicate destination IDs")
	}
}

func TestReporterRequiredPanelACKIgnoresPendingWebhook(t *testing.T) {
	q, err := NewOutbox(t.TempDir(), Limits{MaxItems: 120, MaxBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	panel := &switchSink{}
	webhook := &switchSink{err: errors.New("offline")}
	r, err := NewReporter(q, []Destination{{ID: "panel", Sink: panel}, {ID: "webhook:0", Sink: webhook}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SendRequired(context.Background(), []byte(`{"node_id":"n1"}`), "panel"); err != nil {
		t.Fatalf("Panel was acknowledged: %v", err)
	}
	items, err := q.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Pending) != 1 || items[0].Pending[0] != "webhook:0" {
		t.Fatalf("items=%+v", items)
	}

	panel.setError(errors.New("panel offline"))
	if err := r.SendRequired(context.Background(), []byte(`{"node_id":"n1"}`), "panel"); !errors.Is(err, ErrRequiredDestinationPending) {
		t.Fatalf("required Panel error=%v", err)
	}
}
