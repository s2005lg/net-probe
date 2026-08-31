package control

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
)

func TestHubCapacityOneThousandSessions(t *testing.T) {
	if os.Getenv("NET_PROBE_CAPACITY") != "1" {
		t.Skip("set NET_PROBE_CAPACITY=1 for the tag/manual capacity gate")
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	hub := NewHub(1000, 32)
	sessions := make([]*fakeSession, 1000)
	for index := range sessions {
		sessions[index] = newFakeSession(fmt.Sprintf("session-%04d", index), 32)
		if err := hub.Register(fmt.Sprintf("agent-%04d", index), sessions[index]); err != nil {
			t.Fatalf("register %d: %v", index, err)
		}
	}
	if got := len(hub.Snapshot()); got != 1000 {
		t.Fatalf("online sessions=%d, want 1000", got)
	}

	latencies := make([]time.Duration, len(sessions))
	var wg sync.WaitGroup
	for index, session := range sessions {
		wg.Add(1)
		go func(index int, session *fakeSession) {
			defer wg.Done()
			started := time.Now()
			if err := hub.Send(fmt.Sprintf("agent-%04d", index), []byte(`{"type":"command"}`)); err != nil {
				t.Errorf("dispatch %d: %v", index, err)
				return
			}
			<-session.outbound
			latencies[index] = time.Since(started)
		}(index, session)
	}
	wg.Wait()
	sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
	p95 := latencies[(len(latencies)*95+99)/100-1]
	if p95 >= time.Second {
		t.Fatalf("accepted p95=%s, limit=1s", p95)
	}
	if controlproto.OfflineTimeout < 85*time.Second || controlproto.OfflineTimeout > 95*time.Second {
		t.Fatalf("offline timeout=%s, want 90s +/-5s", controlproto.OfflineTimeout)
	}

	for index := range sessions {
		hub.Disconnect(fmt.Sprintf("agent-%04d", index), "capacity_drop")
	}
	if got := len(hub.Snapshot()); got != 0 {
		t.Fatalf("sessions after drop=%d", got)
	}
	recoveryStarted := time.Now()
	for index := range sessions {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			time.Sleep(time.Duration(index%20) * time.Millisecond)
			session := newFakeSession(fmt.Sprintf("reconnected-%04d", index), 32)
			if err := hub.Register(fmt.Sprintf("agent-%04d", index), session); err != nil {
				t.Errorf("reconnect %d: %v", index, err)
			}
		}(index)
	}
	wg.Wait()
	recovery := time.Since(recoveryStarted)
	if got := len(hub.Snapshot()); got != 1000 {
		t.Fatalf("recovered sessions=%d", got)
	}
	if recovery >= 120*time.Second {
		t.Fatalf("recovery=%s, limit=120s", recovery)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	var growth uint64
	if after.Alloc > before.Alloc {
		growth = after.Alloc - before.Alloc
	}
	if growth >= 128<<20 {
		t.Fatalf("incremental heap=%d bytes, limit=%d", growth, 128<<20)
	}
	t.Logf("sessions=1000 accepted_p95=%s recovery=%s incremental_heap=%d", p95, recovery, growth)
}
