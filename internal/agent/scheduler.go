package agent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
)

type collectFunc func(context.Context) error

// Scheduler serializes collection. Trigger is edge-triggered: any number of
// requests received during a collection become exactly one follow-up run.
type Scheduler struct {
	interval  atomic.Int64
	jitter    time.Duration
	collect   collectFunc
	trigger   chan struct{}
	stop      chan struct{}
	stopOnce  sync.Once
	waitersMu sync.Mutex
	waiters   []chan error
}

func NewScheduler(agentID string, interval time.Duration, collect collectFunc) *Scheduler {
	s := &Scheduler{
		jitter:  StableJitter(agentID),
		collect: collect,
		trigger: make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	s.interval.Store(int64(interval))
	return s
}

func StableJitter(agentID string) time.Duration {
	digest := sha256.Sum256([]byte(agentID))
	const span = uint64(10*time.Second) + 1
	return time.Duration(binary.BigEndian.Uint64(digest[:8])%span) - 5*time.Second
}

func (s *Scheduler) Trigger() {
	select {
	case <-s.stop:
		return
	default:
	}
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Request(ctx context.Context) error {
	select {
	case <-s.stop:
		return context.Canceled
	default:
	}
	response := make(chan error, 1)
	s.waitersMu.Lock()
	s.waiters = append(s.waiters, response)
	s.waitersMu.Unlock()
	s.Trigger()
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		s.removeWaiter(response)
		return ctx.Err()
	}
}

// Stop prevents new collections and lets an in-flight collection finish.
func (s *Scheduler) Stop() {
	if s != nil {
		s.stopOnce.Do(func() { close(s.stop) })
	}
}

func (s *Scheduler) SetInterval(interval time.Duration) {
	if interval > 0 {
		s.interval.Store(int64(interval))
		s.Trigger()
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	if s == nil || s.collect == nil {
		return
	}
	defer s.failWaiters(context.Canceled)
	pending := true // collect immediately at startup
	for {
		if pending {
			pending = false
			waiters := s.takeWaiters()
			err := s.collect(ctx)
			for _, waiter := range waiters {
				waiter <- err
			}
			if ctx.Err() != nil {
				return
			}
			select {
			case <-s.stop:
				return
			default:
			}
			select {
			case <-s.trigger:
				pending = true
			default:
			}
			if pending {
				continue
			}
		}

		nextDelay := time.Duration(s.interval.Load()) + s.jitter
		if nextDelay <= 0 {
			nextDelay = time.Millisecond
		}
		timer := time.NewTimer(nextDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-s.stop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-s.trigger:
			if !timer.Stop() {
				<-timer.C
			}
			pending = true
		case <-timer.C:
			pending = true
		}
	}
}

func (s *Scheduler) takeWaiters() []chan error {
	s.waitersMu.Lock()
	defer s.waitersMu.Unlock()
	waiters := s.waiters
	s.waiters = nil
	return waiters
}

func (s *Scheduler) failWaiters(err error) {
	for _, waiter := range s.takeWaiters() {
		waiter <- err
	}
}

func (s *Scheduler) removeWaiter(target chan error) {
	s.waitersMu.Lock()
	defer s.waitersMu.Unlock()
	for index, waiter := range s.waiters {
		if waiter == target {
			s.waiters = append(s.waiters[:index], s.waiters[index+1:]...)
			return
		}
	}
}
