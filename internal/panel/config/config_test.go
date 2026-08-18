package config

import (
	"os"
	"path/filepath"
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
