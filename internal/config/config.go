package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Sink struct {
	Type              string            `toml:"type"`
	URL               string            `toml:"url"`
	Method            string            `toml:"method"`
	Headers           map[string]string `toml:"headers"`
	TokenEnv          string            `toml:"token_env"`
	TokenFile         string            `toml:"token_file"`
	InsecureAllowHTTP bool              `toml:"insecure_allow_http"`
	TLSSkipVerify     bool              `toml:"tls_skip_verify"`
	TLSCAFile         string            `toml:"tls_ca_file"`
}

type AgentConfig struct {
	NodeID   string `toml:"node_id"`
	LogLevel string `toml:"log_level"`
}

type CollectConfig struct {
	DiskMounts []string `toml:"disk_mounts"`
	Upgradable bool     `toml:"upgradable"`
}

type DetectConfig struct {
	Include   []string `toml:"include"`
	CustomDir string   `toml:"custom_dir"`
}

type StatsService struct {
	Endpoint string `toml:"endpoint"`
	Secret   string `toml:"secret"`
}

type StatsConfig struct {
	Services map[string]StatsService `toml:"services"`
}

type Config struct {
	Agent   AgentConfig   `toml:"agent"`
	Sinks   []Sink        `toml:"sink"`
	Collect CollectConfig `toml:"collect"`
	Detect  DetectConfig  `toml:"detect"`
	Stats   StatsConfig   `toml:"stats"`
}

func Default() *Config {
	return &Config{
		Agent: AgentConfig{LogLevel: "info"},
		Collect: CollectConfig{
			DiskMounts: []string{"/"},
			Upgradable: true,
		},
		Detect: DetectConfig{
			Include:   []string{"hysteria2", "xray", "v2ray", "sing-box", "shadowsocks", "trojan", "tuic", "anytls", "generic"},
			CustomDir: "/etc/net-probe/services.d",
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Agent.LogLevel == "" {
		cfg.Agent.LogLevel = "info"
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if len(c.Sinks) == 0 {
		return fmt.Errorf("at least one sink is required")
	}
	for i, s := range c.Sinks {
		if s.Type != "panel" && s.Type != "webhook" {
			return fmt.Errorf("sink %d: unsupported type %q", i, s.Type)
		}
		if s.URL == "" {
			return fmt.Errorf("sink %d: url is required", i)
		}
		if s.Type == "panel" && strings.HasPrefix(s.URL, "http://") && !s.InsecureAllowHTTP {
			if !strings.HasPrefix(s.URL, "http://127.0.0.1") && !strings.HasPrefix(s.URL, "http://localhost") {
				return fmt.Errorf("sink %d: panel requires https unless insecure_allow_http", i)
			}
		}
	}
	return nil
}

func ResolveToken(s Sink) (string, error) {
	if s.TokenEnv != "" {
		if v := os.Getenv(s.TokenEnv); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("token env %q is empty", s.TokenEnv)
	}
	if s.TokenFile != "" {
		b, err := os.ReadFile(s.TokenFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", nil
}
