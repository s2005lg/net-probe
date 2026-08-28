package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	_ = os.WriteFile(path, []byte("[agent]\ntoken = \"t\"\n[admin]\nuser = \"admin\"\n"), 0o600)
	cfg, err := Load(path)
	if err != nil || cfg.Agent.Token != "t" || cfg.Admin.User != "admin" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.ListenAddr != ":8443" {
		t.Fatalf("listen addr = %q", cfg.ListenAddr)
	}
	if cfg.NodeTimeout != "3m" {
		t.Fatalf("node timeout = %q", cfg.NodeTimeout)
	}
	if cfg.Retention.RawDays != 7 || cfg.Retention.HourlyDays != 30 || cfg.Retention.DailyDays != 365 {
		t.Fatalf("retention = %+v", cfg.Retention)
	}
}

func TestGeoDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Geo.Provider != "ipwhois" || cfg.Geo.URL != "https://ipwho.is/{ip}?lang=zh-CN" || cfg.Geo.Timeout != "4s" || cfg.Geo.RefreshInterval != "12h" {
		t.Fatalf("geo defaults = %+v", cfg.Geo)
	}
}

func TestValidateRejectsUnsafeGeoURL(t *testing.T) {
	cfg := Default()
	cfg.Geo.URL = "http://geo.example/{ip}"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "geo url") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestValidateRejectsInvalidGeoSettings(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"provider", func(c *Config) { c.Geo.Provider = "other" }},
		{"missing placeholder", func(c *Config) { c.Geo.URL = "https://geo.example/address" }},
		{"repeated placeholder", func(c *Config) { c.Geo.URL = "https://geo.example/{ip}/{ip}" }},
		{"invalid timeout", func(c *Config) { c.Geo.Timeout = "0s" }},
		{"invalid refresh interval", func(c *Config) { c.Geo.RefreshInterval = "never" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.edit(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateAcceptsLocalHTTPGeoURL(t *testing.T) {
	cfg := Default()
	cfg.Geo.URL = "http://127.0.0.1/{ip}"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
