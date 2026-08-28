package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[agent]
node_id = "node-1"
log_level = "debug"

[[sink]]
type = "panel"
url = "https://panel.example.com"
token_env = "NET_PROBE_PANEL_TOKEN"

[[sink]]
type = "webhook"
url = "https://uptime.example/api/push/x"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sinks) != 2 {
		t.Fatalf("sinks = %d", len(cfg.Sinks))
	}
	if cfg.Sinks[0].TokenEnv != "NET_PROBE_PANEL_TOKEN" {
		t.Fatalf("token_env = %q", cfg.Sinks[0].TokenEnv)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsHTTPPanel(t *testing.T) {
	cfg := Default()
	cfg.Sinks = []Sink{{Type: "panel", URL: "http://panel.example.com"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for http panel")
	}
}

func TestStatsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[[sink]]
type = "webhook"
url = "https://example.com/report"

[stats.services.hysteria2]
endpoint = "http://127.0.0.1:9999"
secret = "s3cret"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := cfg.Stats.Services["hysteria2"]
	if !ok || s.Endpoint != "http://127.0.0.1:9999" || s.Secret != "s3cret" {
		t.Fatalf("stats = %+v", cfg.Stats.Services)
	}
}

func TestStatsServiceCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `[agent]
node_id = "n1"
[[sink]]
type = "panel"
url = "https://panel.example"
[stats.services.xray]
enabled = false
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Stats.Services["xray"].Enabled == nil || *cfg.Stats.Services["xray"].Enabled {
		t.Fatalf("enabled = %#v", cfg.Stats.Services["xray"].Enabled)
	}
}

func TestEgressIPDefaults(t *testing.T) {
	cfg := Default()
	got := cfg.Collect.EgressIP
	if !got.Enabled || got.RefreshInterval != "6h" || got.Timeout != "3s" {
		t.Fatalf("egress defaults = %+v", got)
	}
	if !reflect.DeepEqual(got.IPv4Endpoints, []string{"https://api.ipify.org", "https://4.ident.me"}) {
		t.Fatalf("IPv4 endpoints = %v", got.IPv4Endpoints)
	}
	if !reflect.DeepEqual(got.IPv6Endpoints, []string{"https://api6.ipify.org", "https://6.ident.me"}) {
		t.Fatalf("IPv6 endpoints = %v", got.IPv6Endpoints)
	}
}

func TestValidateRejectsUnsafeEgressEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Sinks = []Sink{{Type: "webhook", URL: "https://example.com/report"}}
	cfg.Collect.EgressIP.IPv4Endpoints = []string{"http://public.example/ip"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "egress IPv4 endpoint") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsLocalEgressEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Sinks = []Sink{{Type: "webhook", URL: "https://example.com/report"}}
	cfg.Collect.EgressIP.IPv4Endpoints = []string{"http://127.0.0.1:8080/ip"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
