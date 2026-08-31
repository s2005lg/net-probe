package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s2005lg/net-probe/internal/agent"
	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/detect"
)

func TestEnrollCLIReadsCodeOnlyFromStdinAndPrintsNoSecret(t *testing.T) {
	const code = "one-time-enrollment-secret"
	var got agent.EnrollmentOptions
	called := false
	enroll := func(_ context.Context, _ *http.Client, opts agent.EnrollmentOptions) (*agent.Identity, error) {
		called = true
		got = opts
		return &agent.Identity{AgentID: "123e4567-e89b-42d3-a456-426614174020", TLSConfig: &tls.Config{}, CommandKey: make(ed25519.PublicKey, ed25519.PublicKeySize), ReleaseKey: make(ed25519.PublicKey, ed25519.PublicKeySize)}, nil
	}
	var stdout, stderr strings.Builder
	rc := runCLI([]string{"enroll", "--panel-url", "https://panel.example.com:8443", "--ca-fingerprint", strings.Repeat("a", 64), "--code-stdin"}, strings.NewReader(code+"\n"), &stdout, &stderr, enroll)
	if rc != 0 || !called {
		t.Fatalf("rc=%d called=%v stderr=%q", rc, called, stderr.String())
	}
	if got.Code != code || got.PanelURL != "https://panel.example.com:8443" || got.CAFingerprint != strings.Repeat("a", 64) {
		t.Fatalf("options=%+v", got)
	}
	if strings.Contains(stdout.String(), code) || strings.Contains(stderr.String(), code) || !strings.Contains(stdout.String(), gotIdentityID()) {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestEnrollCLIRejectsCodeArgument(t *testing.T) {
	called := false
	enroll := func(context.Context, *http.Client, agent.EnrollmentOptions) (*agent.Identity, error) {
		called = true
		return nil, nil
	}
	rc := runCLI([]string{"enroll", "--panel-url", "https://panel.example.com", "--ca-fingerprint", strings.Repeat("a", 64), "--code", "secret"}, strings.NewReader(""), io.Discard, io.Discard, enroll)
	if rc == 0 || called {
		t.Fatalf("rc=%d called=%v", rc, called)
	}
}

func TestEnrollCLIUsesConfiguredNodeID(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	configDir := filepath.Join(configHome, "net-probe")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configBody := `[agent]
node_id = "edge-01"
[panel]
url = "https://old-panel.example.com"
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	var got agent.EnrollmentOptions
	enroll := func(_ context.Context, _ *http.Client, opts agent.EnrollmentOptions) (*agent.Identity, error) {
		got = opts
		return &agent.Identity{AgentID: gotIdentityID()}, nil
	}
	rc := runCLI([]string{"enroll", "--panel-url", "https://panel.example.com", "--ca-fingerprint", strings.Repeat("a", 64), "--code-stdin"}, strings.NewReader("secret\n"), io.Discard, io.Discard, enroll)
	if rc != 0 || got.NodeID != "edge-01" {
		t.Fatalf("rc=%d options=%+v", rc, got)
	}
}

func gotIdentityID() string { return "123e4567-e89b-42d3-a456-426614174020" }

type fakeResident struct{ ran bool }

func (r *fakeResident) Run(context.Context) error {
	r.ran = true
	return nil
}

func TestCLIUsesResidentModeByDefaultAndOnceOnlyWhenRequested(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	configDir := filepath.Join(configHome, "net-probe")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	writeCLIConfig(t, configPath)
	resident := &fakeResident{}
	onceCalls := 0
	deps := cliDependencies{
		runOnce: func(context.Context, *config.Config, string, detect.Runner) int {
			onceCalls++
			return 0
		},
		newResident: func(string, *config.Config, string, detect.Runner) (residentRunner, error) {
			return resident, nil
		},
	}
	if rc := runCLIWithDependencies(nil, strings.NewReader(""), io.Discard, io.Discard, nil, deps); rc != 0 {
		t.Fatalf("resident rc=%d", rc)
	}
	if !resident.ran || onceCalls != 0 {
		t.Fatalf("resident=%v once=%d", resident.ran, onceCalls)
	}

	resident.ran = false
	if rc := runCLIWithDependencies([]string{"--once"}, strings.NewReader(""), io.Discard, io.Discard, nil, deps); rc != 0 {
		t.Fatalf("once rc=%d", rc)
	}
	if resident.ran || onceCalls != 1 {
		t.Fatalf("resident=%v once=%d", resident.ran, onceCalls)
	}
}

func TestCLIPreflightValidatesReportAndControlWithoutStartingResident(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	configDir := filepath.Join(configHome, "net-probe")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	writeCLIConfig(t, configPath)
	resident := &fakeResident{}
	preflightCalls := 0
	onceCalls := 0
	deps := cliDependencies{
		runOnce: func(context.Context, *config.Config, string, detect.Runner) int {
			onceCalls++
			return 0
		},
		runPreflight: func(context.Context, *config.Config, string, detect.Runner) error {
			preflightCalls++
			return nil
		},
		newResident: func(string, *config.Config, string, detect.Runner) (residentRunner, error) {
			return resident, nil
		},
	}
	if rc := runCLIWithDependencies([]string{"--preflight"}, strings.NewReader(""), io.Discard, io.Discard, nil, deps); rc != 0 {
		t.Fatalf("preflight rc=%d", rc)
	}
	if preflightCalls != 1 || onceCalls != 0 || resident.ran {
		t.Fatalf("preflight=%d once=%d resident=%v", preflightCalls, onceCalls, resident.ran)
	}
	if rc := runCLIWithDependencies([]string{"--preflight", "--once"}, strings.NewReader(""), io.Discard, io.Discard, nil, deps); rc == 0 {
		t.Fatal("combined preflight and once was accepted")
	}
}

func writeCLIConfig(t *testing.T, path string) {
	t.Helper()
	body := `[agent]
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
