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
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "g-" + hex.EncodeToString(b)
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
	tmpls, err := allTemplates(cfg)
	if err != nil {
		return nil, err
	}
	reg, err := detect.NewRegistry(tmpls)
	if err != nil {
		return nil, err
	}
	svcs, err := detect.Detect(ctx, reg, cfg.Detect, detect.Deps{Runner: runner, ProcRoot: "/proc"})
	if err != nil {
		return nil, err
	}
	host, err := collect.Host(ctx, cfg.Collect, runner)
	if err != nil {
		return nil, err
	}
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
	start := time.Now()
	rep, err := Build(ctx, cfg, version, runner)
	if err != nil {
		return 2
	}
	rep.CollectMS = time.Since(start).Milliseconds()
	body, err := json.Marshal(rep)
	if err != nil {
		return 2
	}
	rc := 0
	for _, sc := range cfg.Sinks {
		s, err := sink.New(sc)
		if err != nil {
			rc = 1
			continue
		}
		if err := sendWithRetry(ctx, s, body); err != nil {
			fmt.Fprintf(os.Stderr, "sink %s failed: %v\n", sc.URL, err)
			rc = 1
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
