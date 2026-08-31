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
