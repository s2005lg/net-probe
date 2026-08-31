package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type blockingCollector struct {
	started chan struct{}
	release chan struct{}
	runs    atomic.Int32
}

func newBlockingCollector() *blockingCollector {
	return &blockingCollector{started: make(chan struct{}, 8), release: make(chan struct{}, 8)}
}

func (c *blockingCollector) Run(ctx context.Context) error {
	c.runs.Add(1)
	select {
	case c.started <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *blockingCollector) WaitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-c.started:
	case <-time.After(time.Second):
		t.Fatal("collector did not start")
	}
}

func (c *blockingCollector) Release() { c.release <- struct{}{} }

func (c *blockingCollector) WaitRuns(t *testing.T, want int32) {
	t.Helper()
	deadline := time.After(time.Second)
	for c.runs.Load() < want {
		select {
		case <-c.started:
		case <-deadline:
			t.Fatalf("runs=%d want=%d", c.runs.Load(), want)
		}
	}
}

func (c *blockingCollector) Runs() int32 { return c.runs.Load() }

func TestSchedulerCoalescesWhileRunning(t *testing.T) {
	c := newBlockingCollector()
	s := NewScheduler("agent-1", time.Hour, c.Run)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	c.WaitStarted(t)

	s.Trigger()
	s.Trigger()
	c.Release()
	c.WaitRuns(t, 2)
	if c.Runs() != 2 {
		t.Fatalf("runs=%d", c.Runs())
	}
	c.Release()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestSchedulerJitterIsStableAndBounded(t *testing.T) {
	a := StableJitter("agent-1")
	b := StableJitter("agent-1")
	if a != b || a < -5*time.Second || a > 5*time.Second {
		t.Fatalf("jitter=%s repeat=%s", a, b)
	}
}
