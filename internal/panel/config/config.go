package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	ListenAddr string `toml:"listen_addr"`
	DataDir    string `toml:"data_dir"`
	PublicURL  string `toml:"public_url"`
	Control    struct {
		MaxConnections int `toml:"max_connections"`
		SendQueue      int `toml:"send_queue"`
		MessageRate    int `toml:"message_rate"`
	} `toml:"control"`
	Agent struct {
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
		Provider        string `toml:"provider"`
		URL             string `toml:"url"`
		Timeout         string `toml:"timeout"`
		TokenEnv        string `toml:"token_env"`
		RefreshInterval string `toml:"refresh_interval"`
	} `toml:"geo"`
}

func Default() *Config {
	c := &Config{ListenAddr: ":8443", DataDir: "/var/lib/net-probe-panel", NodeTimeout: "3m"}
	c.Control.MaxConnections, c.Control.SendQueue, c.Control.MessageRate = 1000, 32, 120
	c.Admin.User = "admin"
	c.Retention.RawDays, c.Retention.HourlyDays, c.Retention.DailyDays = 7, 30, 365
	c.Alert.CertExpiryDays, c.Alert.DiskUsagePct, c.Alert.MemUsagePct = 7, 85, 90
	c.Geo.Provider = "ipwhois"
	c.Geo.URL = "https://ipwho.is/{ip}?lang=zh-CN"
	c.Geo.Timeout = "4s"
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

func (c *Config) Validate() error {
	if err := validatePublicURL(c.PublicURL); err != nil {
		return err
	}
	if c.Control.MaxConnections < 1 || c.Control.MaxConnections > 1000 {
		return fmt.Errorf("control max connections must be between 1 and 1000")
	}
	if c.Control.SendQueue < 1 || c.Control.SendQueue > 32 {
		return fmt.Errorf("control send queue must be between 1 and 32")
	}
	if c.Control.MessageRate < 1 || c.Control.MessageRate > 120 {
		return fmt.Errorf("control message rate must be between 1 and 120 per minute")
	}
	if c.Geo.Provider != "ipwhois" {
		return fmt.Errorf("geo provider must be ipwhois")
	}
	if strings.Count(c.Geo.URL, "{ip}") != 1 {
		return fmt.Errorf("geo url must contain exactly one {ip} placeholder")
	}
	u, err := url.Parse(c.Geo.URL)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return fmt.Errorf("geo url is invalid")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(scheme == "http" && isLocalHost(u.Hostname())) {
		return fmt.Errorf("geo url must use https (or local http)")
	}
	if err := positiveDuration("geo timeout", c.Geo.Timeout); err != nil {
		return err
	}
	return positiveDuration("geo refresh interval", c.Geo.RefreshInterval)
}

func validatePublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || strings.ToLower(u.Scheme) != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("public url must be a valid HTTPS URL with host and port")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("public url must not contain a path, query, or fragment")
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return fmt.Errorf("public url has an invalid port")
		}
	}
	return nil
}

func positiveDuration(name, value string) error {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fmt.Errorf("%s must be a positive duration", name)
	}
	return nil
}

func isLocalHost(hostname string) bool {
	switch strings.ToLower(hostname) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
