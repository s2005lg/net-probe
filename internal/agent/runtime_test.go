package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/config"
)

func TestRuntimeReloadPreservesOldSnapshotOnInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`[agent]
report_interval = "not-a-duration"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldConfig := config.Default()
	oldConfig.Agent.NodeID = "old-node"
	r := &Runtime{
		configPath: path,
		current:    &runtimeSnapshot{cfg: oldConfig},
		build: func(*config.Config) (*runtimeSnapshot, error) {
			t.Fatal("builder called for invalid configuration")
			return nil, nil
		},
	}
	if err := r.Reload(); err == nil {
		t.Fatal("invalid configuration reloaded")
	}
	if got := r.snapshot().cfg.Agent.NodeID; got != "old-node" {
		t.Fatalf("active node=%q", got)
	}
}

func TestRuntimeShutdownAllowsCurrentWorkUntilDeadline(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	cfg := config.Default()
	r := &Runtime{
		current: &runtimeSnapshot{
			cfg: cfg, identity: &Identity{AgentID: "agent-shutdown"}, reporter: &Reporter{},
			reportInterval: time.Hour, collectTimeout: time.Hour, shutdownTimeout: 100 * time.Millisecond,
		},
		collect: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("collection did not start")
	}
	begin := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop")
	}
	if elapsed := time.Since(begin); elapsed < 80*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown elapsed=%s", elapsed)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("current collection was not cancelled at shutdown deadline")
	}
}

func TestRuntimeMaintainsControlSessionAlongsideReporting(t *testing.T) {
	panel := startEnrollmentPanel(t)
	pkiDir := t.TempDir()
	const nodeID = "node-runtime-control"
	identity, err := Enroll(context.Background(), insecureBootstrapClient(), EnrollmentOptions{
		PanelURL: panel.URL, CAFingerprint: panel.Fingerprint, Code: panel.Code,
		PKIDir: pkiDir, NodeID: nodeID, Version: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Agent.NodeID = nodeID
	cfg.Agent.ReportInterval = "200ms"
	cfg.Agent.CollectTimeout = "150ms"
	cfg.Agent.ShutdownTimeout = "200ms"
	cfg.Panel.URL = panel.URL
	cfg.Panel.CAFile = filepath.Join(pkiDir, caBundleFile)
	cfg.Panel.CertFile = filepath.Join(pkiDir, agentCertFile)
	cfg.Panel.KeyFile = filepath.Join(pkiDir, agentKeyFile)
	cfg.Panel.CommandKeyFile = filepath.Join(pkiDir, commandKeyFile)
	cfg.Panel.ReleaseKeyFile = filepath.Join(pkiDir, releaseKeyFile)
	cfg.Collect.EgressIP.Enabled = false
	cfg.Detect.CustomDir = t.TempDir()
	runtime, err := NewRuntime(filepath.Join(t.TempDir(), "config.toml"), cfg, "v1", fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var connectedAt, reportAt int64
		_ = panel.DB.QueryRow(`SELECT last_connected_at FROM agent_identities WHERE agent_id=?`, identity.AgentID).Scan(&connectedAt)
		_ = panel.DB.QueryRow(`SELECT COALESCE(last_report_at,0) FROM nodes WHERE node_id=?`, nodeID).Scan(&reportAt)
		if connectedAt > 0 && reportAt > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime did not establish both channels: connected=%d report=%d", connectedAt, reportAt)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop")
	}
}

func TestRuntimeReloadPreservesOldSnapshotUntilReplacementBuildSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeRuntimeConfig(t, path, "new-node")
	oldConfig := config.Default()
	oldConfig.Agent.NodeID = "old-node"
	failBuild := true
	r := &Runtime{
		configPath: path,
		current:    &runtimeSnapshot{cfg: oldConfig},
		build: func(cfg *config.Config) (*runtimeSnapshot, error) {
			if failBuild {
				return nil, errors.New("certificate unavailable")
			}
			return &runtimeSnapshot{cfg: cfg}, nil
		},
	}
	if err := r.Reload(); err == nil {
		t.Fatal("reload succeeded despite replacement build failure")
	}
	if got := r.snapshot().cfg.Agent.NodeID; got != "old-node" {
		t.Fatalf("active node=%q", got)
	}
	failBuild = false
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := r.snapshot().cfg.Agent.NodeID; got != "new-node" {
		t.Fatalf("active node=%q", got)
	}
}

func writeRuntimeConfig(t *testing.T, path, nodeID string) {
	t.Helper()
	body := `[agent]
node_id = "` + nodeID + `"
report_interval = "60s"
collect_timeout = "45s"
shutdown_timeout = "20s"

[panel]
url = "https://panel.example.com"
ca_file = "/tmp/ca.crt"
cert_file = "/tmp/agent.crt"
key_file = "/tmp/agent.key"
command_key_file = "/tmp/command.pub"
release_key_file = "/tmp/release.pub"

[collect.egress_ip]
enabled = false
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
