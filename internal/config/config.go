package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/s2005lg/net-probe/internal/urlpolicy"
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
	NodeID          string `toml:"node_id"`
	LogLevel        string `toml:"log_level"`
	ReportInterval  string `toml:"report_interval"`
	CollectTimeout  string `toml:"collect_timeout"`
	ShutdownTimeout string `toml:"shutdown_timeout"`
}

type PanelConfig struct {
	URL            string `toml:"url"`
	CAFile         string `toml:"ca_file"`
	CertFile       string `toml:"cert_file"`
	KeyFile        string `toml:"key_file"`
	CommandKeyFile string `toml:"command_key_file"`
	ReleaseKeyFile string `toml:"release_key_file"`
}

type EgressIPConfig struct {
	Enabled         bool     `toml:"enabled"`
	RefreshInterval string   `toml:"refresh_interval"`
	Timeout         string   `toml:"timeout"`
	IPv4Endpoints   []string `toml:"ipv4_endpoints"`
	IPv6Endpoints   []string `toml:"ipv6_endpoints"`
}

type CollectConfig struct {
	DiskMounts []string       `toml:"disk_mounts"`
	Upgradable bool           `toml:"upgradable"`
	EgressIP   EgressIPConfig `toml:"egress_ip"`
}

type DetectConfig struct {
	Include   []string `toml:"include"`
	CustomDir string   `toml:"custom_dir"`
}

type StatsService struct {
	Enabled  *bool  `toml:"enabled"`
	Endpoint string `toml:"endpoint"`
	Secret   string `toml:"secret"`
}

type StatsConfig struct {
	Services map[string]StatsService `toml:"services"`
}

type Config struct {
	Agent   AgentConfig   `toml:"agent"`
	Panel   PanelConfig   `toml:"panel"`
	Sinks   []Sink        `toml:"sink"`
	Collect CollectConfig `toml:"collect"`
	Detect  DetectConfig  `toml:"detect"`
	Stats   StatsConfig   `toml:"stats"`
}

func Default() *Config {
	return &Config{
		Agent: AgentConfig{
			LogLevel:        "info",
			ReportInterval:  "60s",
			CollectTimeout:  "45s",
			ShutdownTimeout: "20s",
		},
		Panel: PanelConfig{
			CAFile:         "/etc/net-probe/pki/ca.crt",
			CertFile:       "/etc/net-probe/pki/agent.crt",
			KeyFile:        "/etc/net-probe/pki/agent.key",
			CommandKeyFile: "/etc/net-probe/pki/command-signing.pub",
			ReleaseKeyFile: "/etc/net-probe/pki/release-signing.pub",
		},
		Collect: CollectConfig{
			DiskMounts: []string{"/"},
			Upgradable: true,
			EgressIP: EgressIPConfig{
				Enabled:         true,
				RefreshInterval: "6h",
				Timeout:         "3s",
				IPv4Endpoints:   []string{"https://api.ipify.org", "https://4.ident.me"},
				IPv6Endpoints:   []string{"https://api6.ipify.org", "https://6.ident.me"},
			},
		},
		Detect: DetectConfig{
			Include:   []string{"hysteria2", "xray", "v2ray", "sing-box", "shadowsocks", "trojan", "tuic", "anytls"},
			CustomDir: "/etc/net-probe/services.d",
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	meta, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if unknown := meta.Undecoded(); len(unknown) != 0 {
		return nil, fmt.Errorf("unknown config keys: %v", unknown)
	}
	if cfg.Agent.LogLevel == "" {
		cfg.Agent.LogLevel = "info"
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if err := validatePanel(c.Panel); err != nil {
		return err
	}
	if err := validateResidentTiming(c.Agent); err != nil {
		return err
	}
	for i, s := range c.Sinks {
		if s.Type != "webhook" {
			if s.Type == "panel" {
				return fmt.Errorf("sink %d: panel reporting must use [panel]", i)
			}
			return fmt.Errorf("sink %d: unsupported type %q", i, s.Type)
		}
		if s.URL == "" {
			return fmt.Errorf("sink %d: url is required", i)
		}
	}
	if err := validateEgressIP(c.Collect.EgressIP); err != nil {
		return err
	}
	return nil
}

func validatePanel(panel PanelConfig) error {
	if panel.URL == "" {
		return fmt.Errorf("panel url is required")
	}
	u, err := url.Parse(panel.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return fmt.Errorf("panel url must be a secure https URL")
	}
	for name, path := range map[string]string{
		"CA file":          panel.CAFile,
		"certificate file": panel.CertFile,
		"private key file": panel.KeyFile,
		"command key file": panel.CommandKeyFile,
		"release key file": panel.ReleaseKeyFile,
	} {
		if path == "" {
			return fmt.Errorf("panel %s is required", name)
		}
	}
	return nil
}

func validateResidentTiming(agent AgentConfig) error {
	reportInterval, err := time.ParseDuration(agent.ReportInterval)
	if err != nil || reportInterval <= 0 {
		return fmt.Errorf("agent report interval must be a positive duration")
	}
	collectTimeout, err := time.ParseDuration(agent.CollectTimeout)
	if err != nil || collectTimeout <= 0 {
		return fmt.Errorf("agent collect timeout must be a positive duration")
	}
	shutdownTimeout, err := time.ParseDuration(agent.ShutdownTimeout)
	if err != nil || shutdownTimeout <= 0 {
		return fmt.Errorf("agent shutdown timeout must be a positive duration")
	}
	if collectTimeout >= reportInterval {
		return fmt.Errorf("agent collect timeout must be shorter than report interval")
	}
	return nil
}

func validateEgressIP(cfg EgressIPConfig) error {
	if !cfg.Enabled {
		return nil
	}
	refresh, err := time.ParseDuration(cfg.RefreshInterval)
	if err != nil || refresh <= 0 {
		return fmt.Errorf("egress refresh interval must be a positive duration")
	}
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil || timeout <= 0 {
		return fmt.Errorf("egress timeout must be a positive duration")
	}
	if len(cfg.IPv4Endpoints) == 0 {
		return fmt.Errorf("egress IPv4 endpoint list must not be empty")
	}
	if len(cfg.IPv6Endpoints) == 0 {
		return fmt.Errorf("egress IPv6 endpoint list must not be empty")
	}
	if err := validateEgressEndpoints("IPv4", cfg.IPv4Endpoints); err != nil {
		return err
	}
	return validateEgressEndpoints("IPv6", cfg.IPv6Endpoints)
}

func validateEgressEndpoints(family string, endpoints []string) error {
	for i, raw := range endpoints {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Hostname() == "" {
			return fmt.Errorf("egress %s endpoint %d is invalid", family, i)
		}
		if !urlpolicy.IsSecureEndpoint(u) {
			return fmt.Errorf("egress %s endpoint %d must use https (or local http)", family, i)
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
