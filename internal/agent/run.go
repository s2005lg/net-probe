package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/s2005lg/net-probe/internal/collect"
	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/detect"
	"github.com/s2005lg/net-probe/internal/egressip"
	"github.com/s2005lg/net-probe/internal/logx"
	"github.com/s2005lg/net-probe/internal/report"
	"github.com/s2005lg/net-probe/internal/sink"
)

func NodeID(cfg *config.Config) string {
	if cfg.Agent.NodeID != "" {
		return cfg.Agent.NodeID
	}
	if b, err := os.ReadFile("/etc/machine-id"); err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
			if len(id) > 12 {
				id = id[:12]
			}
			return "m-" + id
		}
	}
	return persistedNodeID()
}

func persistedNodeID() string {
	dir := ConfigDir()
	path := filepath.Join(dir, "node-id")
	if b, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
			return id
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	id := "g-" + hex.EncodeToString(b)
	if err := os.MkdirAll(dir, 0o755); err == nil {
		_ = os.WriteFile(path, []byte(id), 0o644)
	}
	return id
}

func allTemplates(cfg *config.Config) ([]detect.Template, error) {
	builtin, err := detect.Builtin()
	if err != nil {
		return nil, err
	}
	custom, err := detect.LoadCustom(cfg.Detect.CustomDir)
	if err != nil {
		return nil, err
	}
	return append(builtin, custom...), nil
}

func Build(ctx context.Context, cfg *config.Config, version string, runner detect.Runner) (*report.Report, error) {
	return build(ctx, cfg, version, runner, nil)
}

func build(ctx context.Context, cfg *config.Config, version string, runner detect.Runner, logf func(string, ...any)) (*report.Report, error) {
	return buildWithEgress(ctx, cfg, version, runner, logf, func(ctx context.Context, opts egressip.Options) egressip.Result {
		fetcher := egressip.Fetcher{Client: egressip.NewHTTPClient(opts.Timeout)}
		return (egressip.Resolver{Discover: fetcher.Discover}).Resolve(ctx, opts)
	})
}

type resolveEgressFunc func(context.Context, egressip.Options) egressip.Result

func buildWithEgress(
	ctx context.Context,
	cfg *config.Config,
	version string,
	runner detect.Runner,
	logf func(string, ...any),
	resolve resolveEgressFunc,
) (*report.Report, error) {
	tmpls, err := allTemplates(cfg)
	if err != nil {
		return nil, err
	}
	reg, err := detect.NewRegistry(tmpls)
	if err != nil {
		return nil, err
	}
	svcs, err := detect.Detect(ctx, reg, cfg.Detect, cfg.Stats, detect.Deps{Runner: runner, ProcRoot: "/proc", Logf: logf})
	if err != nil {
		return nil, err
	}
	host, err := collect.Host(ctx, cfg.Collect, runner)
	if err != nil {
		return nil, err
	}
	refreshInterval, err := time.ParseDuration(cfg.Collect.EgressIP.RefreshInterval)
	if err != nil && cfg.Collect.EgressIP.Enabled {
		return nil, fmt.Errorf("parse egress IP refresh interval: %w", err)
	}
	timeout, err := time.ParseDuration(cfg.Collect.EgressIP.Timeout)
	if err != nil && cfg.Collect.EgressIP.Enabled {
		return nil, fmt.Errorf("parse egress IP timeout: %w", err)
	}
	egress := resolve(ctx, egressip.Options{
		Enabled:         cfg.Collect.EgressIP.Enabled,
		RefreshInterval: refreshInterval,
		Timeout:         timeout,
		CachePath:       filepath.Join(ConfigDir(), "egress-ip-cache.json"),
		IPv4Endpoints:   cfg.Collect.EgressIP.IPv4Endpoints,
		IPv6Endpoints:   cfg.Collect.EgressIP.IPv6Endpoints,
		Logf:            logf,
	})
	host.EgressIPv4 = egress.IPv4
	host.EgressIPv6 = egress.IPv6
	return &report.Report{
		SchemaVersion: "1",
		AgentVersion:  version,
		NodeID:        NodeID(cfg),
		CollectedAt:   time.Now().Format(time.RFC3339),
		Host:          host,
		Services:      svcs,
	}, nil
}

func Run(ctx context.Context, cfg *config.Config, version string, runner detect.Runner) int {
	logger := logx.New(cfg.Agent.LogLevel)
	start := time.Now()
	rep, err := build(ctx, cfg, version, runner, logger.Debugf)
	if err != nil {
		logger.Errorf("build report: %v", err)
		return 2
	}
	rep.CollectMS = time.Since(start).Milliseconds()
	logger.Debugf("built report node=%s services=%d collect_ms=%d", rep.NodeID, len(rep.Services), rep.CollectMS)
	body, err := json.Marshal(rep)
	if err != nil {
		logger.Errorf("marshal report: %v", err)
		return 2
	}
	rc := 0
	if cfg.Panel.URL != "" {
		identity, err := LoadIdentity(cfg.Panel.URL, filepath.Dir(cfg.Panel.CAFile))
		if err != nil {
			logger.Errorf("load Panel identity: %v", err)
			rc = 1
		} else if err := RenewIfNeeded(ctx, identity, time.Now()); err != nil {
			logger.Errorf("renew Panel identity: %v", err)
			rc = 1
		} else {
			panelSink, err := sink.NewPanel(cfg.Panel.URL, identity.TLSConfig)
			if err != nil {
				logger.Errorf("init Panel sink: %v", err)
				rc = 1
			} else if err := sendWithRetry(ctx, panelSink, body); err != nil {
				logger.Errorf("Panel sink failed: %v", err)
				rc = 1
			} else {
				logger.Debugf("Panel sink ok")
			}
		}
	}
	for _, sc := range cfg.Sinks {
		s, err := sink.New(sc, rep.NodeID)
		if err != nil {
			logger.Errorf("init sink %s: %v", sc.URL, err)
			rc = 1
			continue
		}
		if err := sendWithRetry(ctx, s, body); err != nil {
			logger.Errorf("sink %s failed: %v", sc.URL, err)
			rc = 1
		} else {
			logger.Debugf("sink %s ok", sc.URL)
		}
	}
	return rc
}

func sendWithRetry(ctx context.Context, s sink.Sink, body []byte) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = s.Send(ctx, body); err == nil {
			return nil
		}
	}
	return err
}

func ConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "net-probe")
	}
	return "/etc/net-probe"
}
