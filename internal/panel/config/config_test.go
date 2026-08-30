package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	_ = os.WriteFile(path, []byte("public_url = \"https://panel.example.com:24443\"\n[agent]\ntoken = \"t\"\n[admin]\nuser = \"admin\"\n"), 0o600)
	cfg, err := Load(path)
	if err != nil || cfg.PublicURL != "https://panel.example.com:24443" || cfg.Agent.Token != "t" || cfg.Admin.User != "admin" {
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
	if cfg.Control.MaxConnections != 1000 || cfg.Control.SendQueue != 32 || cfg.Control.MessageRate != 120 {
		t.Fatalf("control limits = %+v", cfg.Control)
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
	cfg := validConfig()
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
			cfg := validConfig()
			tt.edit(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateAcceptsLocalHTTPGeoURL(t *testing.T) {
	cfg := validConfig()
	cfg.Geo.URL = "http://127.0.0.1/{ip}"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequiresSecurePublicURLWithValidHostAndPort(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "missing"},
		{name: "insecure", url: "http://panel.example.com:24443"},
		{name: "missing host", url: "https:///panel"},
		{name: "invalid port", url: "https://panel.example.com:99999"},
		{name: "path", url: "https://panel.example.com:24443/control"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.PublicURL = tt.url
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "public url") {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

func TestValidateAcceptsSecurePublicURLWithDefaultOrExplicitPort(t *testing.T) {
	for _, publicURL := range []string{"https://panel.example.com", "https://198.51.100.8:24443"} {
		cfg := validConfig()
		cfg.PublicURL = publicURL
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate(%q) = %v", publicURL, err)
		}
	}
}

func TestValidateRejectsUnboundedControlLimits(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{name: "zero connections", edit: func(c *Config) { c.Control.MaxConnections = 0 }},
		{name: "too many connections", edit: func(c *Config) { c.Control.MaxConnections = 1001 }},
		{name: "zero send queue", edit: func(c *Config) { c.Control.SendQueue = 0 }},
		{name: "oversized send queue", edit: func(c *Config) { c.Control.SendQueue = 33 }},
		{name: "zero message rate", edit: func(c *Config) { c.Control.MessageRate = 0 }},
		{name: "excessive message rate", edit: func(c *Config) { c.Control.MessageRate = 121 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.edit(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("accepted unbounded control limit")
			}
		})
	}
}

func validConfig() *Config {
	cfg := Default()
	cfg.PublicURL = "https://panel.example.com:24443"
	return cfg
}
