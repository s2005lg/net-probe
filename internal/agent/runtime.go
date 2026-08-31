package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/detect"
	"github.com/s2005lg/net-probe/internal/logx"
	"github.com/s2005lg/net-probe/internal/sink"
)

type runtimeSnapshot struct {
	cfg             *config.Config
	identity        *Identity
	reporter        *Reporter
	reportInterval  time.Duration
	collectTimeout  time.Duration
	shutdownTimeout time.Duration
}

type snapshotBuilder func(*config.Config) (*runtimeSnapshot, error)

type Runtime struct {
	mu         sync.RWMutex
	configPath string
	current    *runtimeSnapshot
	build      snapshotBuilder
	version    string
	runner     detect.Runner
	logger     *logx.Logger
	scheduler  *Scheduler
	collect    collectFunc
}

func NewRuntime(configPath string, cfg *config.Config, version string, runner detect.Runner) (*Runtime, error) {
	if cfg == nil || runner == nil {
		return nil, errors.New("runtime configuration and detector runner are required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	outbox, err := NewOutbox(StateDir(), Limits{MaxItems: 120, MaxBytes: 16 << 20})
	if err != nil {
		return nil, fmt.Errorf("initialize report outbox: %w", err)
	}
	runtime := &Runtime{configPath: configPath, version: version, runner: runner, logger: logx.New(cfg.Agent.LogLevel)}
	runtime.build = func(candidate *config.Config) (*runtimeSnapshot, error) {
		return runtime.buildSnapshot(candidate, outbox)
	}
	runtime.collect = runtime.collectOnce
	snapshot, err := runtime.build(cfg)
	if err != nil {
		return nil, err
	}
	runtime.current = snapshot
	return runtime, nil
}

func (r *Runtime) buildSnapshot(cfg *config.Config, outbox *Outbox) (*runtimeSnapshot, error) {
	reportInterval, err := time.ParseDuration(cfg.Agent.ReportInterval)
	if err != nil {
		return nil, err
	}
	collectTimeout, err := time.ParseDuration(cfg.Agent.CollectTimeout)
	if err != nil {
		return nil, err
	}
	shutdownTimeout, err := time.ParseDuration(cfg.Agent.ShutdownTimeout)
	if err != nil {
		return nil, err
	}
	identity, err := LoadIdentity(cfg.Panel.URL, filepath.Dir(cfg.Panel.CAFile))
	if err != nil {
		return nil, fmt.Errorf("load Panel identity: %w", err)
	}
	if err := RenewIfNeeded(context.Background(), identity, time.Now()); err != nil {
		return nil, fmt.Errorf("renew Panel identity: %w", err)
	}
	panelSink, err := sink.NewPanel(cfg.Panel.URL, identity.TLSConfig)
	if err != nil {
		return nil, err
	}
	destinations := []Destination{{ID: "panel", Sink: panelSink}}
	for index, sinkConfig := range cfg.Sinks {
		webhook, err := sink.New(sinkConfig, NodeID(cfg))
		if err != nil {
			return nil, fmt.Errorf("initialize webhook %d: %w", index, err)
		}
		destinations = append(destinations, Destination{ID: "webhook:" + strconv.Itoa(index), Sink: webhook})
	}
	reporter, err := NewReporter(outbox, destinations)
	if err != nil {
		return nil, err
	}
	return &runtimeSnapshot{
		cfg: cfg, identity: identity, reporter: reporter, reportInterval: reportInterval,
		collectTimeout: collectTimeout, shutdownTimeout: shutdownTimeout,
	}, nil
}

func (r *Runtime) snapshot() *runtimeSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

func (r *Runtime) Reload() error {
	candidate, err := config.Load(r.configPath)
	if err != nil {
		return err
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	replacement, err := r.build(candidate)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.current = replacement
	scheduler := r.scheduler
	r.mu.Unlock()
	if scheduler != nil {
		scheduler.SetInterval(replacement.reportInterval)
	}
	return nil
}

func (r *Runtime) Run(ctx context.Context) error {
	snapshot := r.snapshot()
	if snapshot == nil || snapshot.identity == nil || snapshot.reporter == nil {
		return errors.New("runtime is not initialized")
	}
	collect := r.collect
	if collect == nil {
		collect = r.collectOnce
	}
	scheduler := NewScheduler(snapshot.identity.AgentID, snapshot.reportInterval, collect)
	r.mu.Lock()
	r.scheduler = scheduler
	r.mu.Unlock()
	runContext, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	if err := NotifyReady(); err != nil {
		return fmt.Errorf("notify ready: %w", err)
	}
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		r.watchdog(runContext)
	}()
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		scheduler.Run(runContext)
	}()
	select {
	case <-ctx.Done():
		_ = NotifyStopping()
		scheduler.Stop()
		grace := snapshot.shutdownTimeout
		if latest := r.snapshot(); latest != nil && latest.shutdownTimeout > 0 {
			grace = latest.shutdownTimeout
		}
		timer := time.NewTimer(grace)
		select {
		case <-schedulerDone:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			cancelRun()
			<-schedulerDone
		}
	case <-schedulerDone:
	}
	cancelRun()
	r.mu.Lock()
	if r.scheduler == scheduler {
		r.scheduler = nil
	}
	r.mu.Unlock()
	<-watchdogDone
	return nil
}

func (r *Runtime) CollectNow() error {
	r.mu.RLock()
	scheduler := r.scheduler
	r.mu.RUnlock()
	if scheduler == nil {
		return errors.New("runtime is not running")
	}
	scheduler.Trigger()
	return nil
}

func (r *Runtime) collectOnce(parent context.Context) error {
	snapshot := r.snapshot()
	if snapshot == nil {
		return errors.New("runtime snapshot unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, snapshot.collectTimeout)
	defer cancel()
	started := time.Now()
	report, err := build(ctx, snapshot.cfg, r.version, r.runner, r.logger.Debugf)
	if err != nil {
		r.logger.Errorf("build report: %v", err)
		return err
	}
	report.CollectMS = time.Since(started).Milliseconds()
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err := snapshot.reporter.Send(ctx, body); err != nil {
		r.logger.Warnf("report queued for retry")
		return err
	}
	r.logger.Debugf("reported node=%s services=%d collect_ms=%d", report.NodeID, len(report.Services), report.CollectMS)
	return nil
}

func (r *Runtime) watchdog(ctx context.Context) {
	interval := watchdogInterval()
	if interval <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := NotifyWatchdog(); err != nil && r.logger != nil {
				r.logger.Warnf("systemd watchdog notification failed")
			}
		}
	}
}

func watchdogInterval() time.Duration {
	raw := os.Getenv("WATCHDOG_USEC")
	if raw == "" {
		return 0
	}
	microseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || microseconds <= 0 {
		return 0
	}
	return time.Duration(microseconds) * time.Microsecond / 2
}

func StateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "net-probe")
	}
	return "/var/lib/net-probe"
}
