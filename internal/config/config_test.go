package config

import (
	"os"
	"path/filepath"
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
