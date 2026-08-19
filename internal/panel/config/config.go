package config

import "github.com/BurntSushi/toml"

type Config struct {
	ListenAddr string `toml:"listen_addr"`
	DataDir    string `toml:"data_dir"`
	Agent      struct {
		Token string `toml:"token"`
	} `toml:"agent"`
	Admin struct {
		User string `toml:"user"`
	} `toml:"admin"`
	NodeTimeout string `toml:"node_timeout"`
	Retention   struct {
		RawDays    int `toml:"raw_days"`
		HourlyDays int `toml:"hourly_days"`
		DailyDays  int `toml:"daily_days"`
	} `toml:"retention"`
	Alert struct {
		CertExpiryDays int    `toml:"cert_expiry_days"`
		DiskUsagePct   int    `toml:"disk_usage_pct"`
		MemUsagePct    int    `toml:"mem_usage_pct"`
		TelegramToken  string `toml:"telegram_token"`
		TelegramChatID string `toml:"telegram_chat_id"`
		WebhookURL     string `toml:"webhook_url"`
	} `toml:"alert"`
	Geo struct {
		RefreshInterval string `toml:"refresh_interval"`
	} `toml:"geo"`
}

func Default() *Config {
	c := &Config{ListenAddr: ":8443", DataDir: "/var/lib/net-probe-panel", NodeTimeout: "3m"}
	c.Admin.User = "admin"
	c.Retention.RawDays, c.Retention.HourlyDays, c.Retention.DailyDays = 7, 30, 365
	c.Alert.CertExpiryDays, c.Alert.DiskUsagePct, c.Alert.MemUsagePct = 7, 85, 90
	c.Geo.RefreshInterval = "12h"
	return c
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
