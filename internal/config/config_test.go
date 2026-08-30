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

[panel]
url = "https://panel.example.com"

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
	if len(cfg.Sinks) != 1 {
		t.Fatalf("sinks = %d", len(cfg.Sinks))
	}
	if cfg.Panel.URL != "https://panel.example.com" {
		t.Fatalf("panel url = %q", cfg.Panel.URL)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsHTTPPanel(t *testing.T) {
	cfg := Default()
	cfg.Panel.URL = "http://panel.example.com"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for http panel")
	}
}

func TestStatsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[panel]
url = "https://panel.example.com"

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
[panel]
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
	cfg.Panel.URL = "https://panel.example.com"
	cfg.Collect.EgressIP.IPv4Endpoints = []string{"http://public.example/ip"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "egress IPv4 endpoint") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsLocalEgressEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Panel.URL = "https://panel.example.com"
	cfg.Collect.EgressIP.IPv4Endpoints = []string{"http://127.0.0.1:8080/ip"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestResidentDefaults(t *testing.T) {
	c := Default()
	if c.Agent.ReportInterval != "60s" || c.Agent.CollectTimeout != "45s" || c.Agent.ShutdownTimeout != "20s" {
		t.Fatalf("agent=%+v", c.Agent)
	}
}

func TestPanelPKIDefaults(t *testing.T) {
	c := Default()
	if c.Panel.CAFile != "/etc/net-probe/pki/ca.crt" ||
		c.Panel.CertFile != "/etc/net-probe/pki/agent.crt" ||
		c.Panel.KeyFile != "/etc/net-probe/pki/agent.key" ||
		c.Panel.CommandKeyFile != "/etc/net-probe/pki/command-signing.pub" ||
		c.Panel.ReleaseKeyFile != "/etc/net-probe/pki/release-signing.pub" {
		t.Fatalf("panel defaults=%+v", c.Panel)
	}
}

func TestValidateRequiresSecurePanelURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "missing"},
		{name: "insecure", url: "http://panel.example.com"},
		{name: "missing host", url: "https:///control"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Panel.URL = tt.url
			if err := cfg.Validate(); err == nil {
				t.Fatal("accepted insecure panel URL")
			}
		})
	}
}

func TestValidateResidentTiming(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AgentConfig)
	}{
		{name: "non-positive report interval", mutate: func(c *AgentConfig) { c.ReportInterval = "0s" }},
		{name: "non-positive collect timeout", mutate: func(c *AgentConfig) { c.CollectTimeout = "0s" }},
		{name: "non-positive shutdown timeout", mutate: func(c *AgentConfig) { c.ShutdownTimeout = "0s" }},
		{name: "collection equals report interval", mutate: func(c *AgentConfig) { c.CollectTimeout = c.ReportInterval }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Panel.URL = "https://panel.example.com"
			tt.mutate(&cfg.Agent)
			if err := cfg.Validate(); err == nil {
				t.Fatal("accepted invalid resident timing")
			}
		})
	}
}

func TestLoadRejectsPanelTokenAndSkipVerifyConfiguration(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "token environment", key: `token_env = "NET_PROBE_PANEL_TOKEN"`},
		{name: "token file", key: `token_file = "/run/secrets/panel-token"`},
		{name: "skip verify", key: "tls_skip_verify = true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			content := "[panel]\nurl = \"https://panel.example.com\"\n" + tt.key + "\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("accepted forbidden Panel credential or TLS configuration")
			}
		})
	}
}

func TestValidateRejectsLegacyPanelSink(t *testing.T) {
	cfg := Default()
	cfg.Panel.URL = "https://panel.example.com"
	cfg.Sinks = []Sink{{Type: "panel", URL: "https://panel.example.com"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted legacy panel sink")
	}
}
