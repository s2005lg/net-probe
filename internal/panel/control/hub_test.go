package control

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type fakeSession struct {
	id       string
	outbound chan []byte
	mu       sync.Mutex
	reason   string
}

func newFakeSession(id string, queue ...int) *fakeSession {
	size := 32
	if len(queue) > 0 {
		size = queue[0]
	}
	return &fakeSession{id: id, outbound: make(chan []byte, size)}
}

func (s *fakeSession) SessionID() string       { return s.id }
func (s *fakeSession) Outbound() chan<- []byte { return s.outbound }
func (s *fakeSession) Close(reason string) {
	s.mu.Lock()
	s.reason = reason
	s.mu.Unlock()
}
func (s *fakeSession) ClosedWith(reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason == reason
}

func TestRegisterReplacesOlderSession(t *testing.T) {
	h := NewHub(1000, 32)
	old, newer := newFakeSession("old"), newFakeSession("new")
	if err := h.Register("a1", old); err != nil {
		t.Fatal(err)
	}
	if err := h.Register("a1", newer); err != nil {
		t.Fatal(err)
	}
	if !old.ClosedWith("replaced") || h.SessionID("a1") != "new" {
		t.Fatal("replacement failed")
	}
}

func TestHubRejectsCapacityAndReportsBackpressure(t *testing.T) {
	h := NewHub(2, 1)
	first := newFakeSession("s1", 1)
	if err := h.Register("a1", first); err != nil {
		t.Fatal(err)
	}
	if err := h.Register("a2", newFakeSession("s2", 1)); err != nil {
		t.Fatal(err)
	}
	if err := h.Register("a3", newFakeSession("s3", 1)); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity err=%v", err)
	}
	if err := h.Send("a1", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := h.Send("a1", []byte("two")); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("backpressure err=%v", err)
	}
}

func TestHubDisconnectClosesAndRemovesCurrentSession(t *testing.T) {
	h := NewHub(2, 1)
	session := newFakeSession("s1", 1)
	if err := h.Register("a1", session); err != nil {
		t.Fatal(err)
	}
	if !h.Disconnect("a1", "revoked") || h.IsOnline("a1") || !session.ClosedWith("revoked") {
		t.Fatalf("online=%v closed=%v", h.IsOnline("a1"), session.ClosedWith("revoked"))
	}
}

func TestHubRegisterSendUnregisterConcurrent(t *testing.T) {
	h := NewHub(1000, 32)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			agentID := fmt.Sprintf("a-%d", i%20)
			session := newFakeSession(fmt.Sprintf("s-%d", i), 128)
			_ = h.Register(agentID, session)
			_ = h.Send(agentID, []byte("message"))
			h.Unregister(agentID, session.SessionID())
		}(i)
	}
	wg.Wait()
}
