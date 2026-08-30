package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/alert"
	"github.com/s2005lg/net-probe/internal/panel/api"
	"github.com/s2005lg/net-probe/internal/panel/config"
	"github.com/s2005lg/net-probe/internal/panel/db"
	"github.com/s2005lg/net-probe/internal/panel/geo"
	"github.com/s2005lg/net-probe/internal/panel/pki"
	"github.com/s2005lg/net-probe/internal/panel/retention"
	panelversion "github.com/s2005lg/net-probe/internal/panel/version"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/net-probe-panel/config.toml", "config file path")
	ver := flag.Bool("version", false, "print version")
	flag.Parse()
	if *ver {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("validate config: %v", err)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "/var/lib/net-probe-panel"
	}

	d, err := db.Open(filepath.Join(cfg.DataDir, "net-probe-panel.db"))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer d.Close()
	if err := db.Migrate(d); err != nil {
		log.Fatalf("migrate db: %v", err)
	}
	refresher, err := newGeoRefresher(d, cfg)
	if err != nil {
		log.Fatalf("configure geolocation: %v", err)
	}

	var users int
	if err := d.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil {
		log.Fatalf("count users: %v", err)
	}
	if users == 0 {
		password := os.Getenv("NET_PROBE_PANEL_ADMIN_PASSWORD")
		if password == "" {
			log.Fatal("NET_PROBE_PANEL_ADMIN_PASSWORD is required when the users table is empty")
		}
		if err := api.EnsureAdmin(d, cfg.Admin.User, password); err != nil {
			log.Fatalf("create admin: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refresher.Run(ctx)
	startBackground(ctx, d, cfg, refresher)

	apiServer := api.New(d, cfg, refresher)
	apiServer.ConfigPath = *cfgPath
	srv, manager, err := newPanelTLSServer(cfg, apiServer.Routes())
	if err != nil {
		log.Fatalf("configure private PKI: %v", err)
	}
	log.Printf("net-probe-panel listening on %s", cfg.ListenAddr)
	if err := srv.ListenAndServeTLS(manager.ServerCertFile, manager.ServerKeyFile); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func newPanelTLSServer(cfg *config.Config, handler http.Handler) (*http.Server, *pki.Manager, error) {
	manager, err := pki.Ensure(cfg.DataDir, cfg.PublicURL)
	if err != nil {
		return nil, nil, err
	}
	return api.NewTLSServer(cfg.ListenAddr, handler, manager.TLSConfig()), manager, nil
}

func newGeoRefresher(d *sql.DB, cfg *config.Config) (*geo.Refresher, error) {
	var token string
	if cfg.Geo.TokenEnv != "" {
		token = os.Getenv(cfg.Geo.TokenEnv)
		if token == "" {
			return nil, fmt.Errorf("geo token env %q is empty", cfg.Geo.TokenEnv)
		}
	}
	timeout, err := time.ParseDuration(cfg.Geo.Timeout)
	if err != nil {
		return nil, fmt.Errorf("parse geo timeout: %w", err)
	}
	freshness, err := time.ParseDuration(cfg.Geo.RefreshInterval)
	if err != nil {
		return nil, fmt.Errorf("parse geo freshness: %w", err)
	}
	client := geo.NewHTTPClient(timeout)
	provider := geo.NewIPWhoisProvider(client, cfg.Geo.URL, token)
	return geo.NewRefresher(d, provider, freshness, log.Printf), nil
}

func startBackground(ctx context.Context, d *sql.DB, cfg *config.Config, refresher *geo.Refresher) {
	go runEvery(ctx, time.Hour, func(ctx context.Context) {
		if err := retention.Aggregate(ctx, d, cfg.Retention.RawDays, cfg.Retention.HourlyDays, cfg.Retention.DailyDays); err != nil {
			log.Printf("retention aggregation: %v", err)
		}
	})
	go runEvery(ctx, time.Minute, func(ctx context.Context) {
		if err := alert.Evaluate(ctx, d, cfg, time.Now()); err != nil {
			log.Printf("alert evaluation: %v", err)
		}
	})
	go runEvery(ctx, geoRefreshInterval(cfg), func(ctx context.Context) {
		if err := refresher.Reconcile(ctx); err != nil {
			log.Printf("IP location reconciliation: %v", err)
		}
	})
	go runEvery(ctx, 12*time.Hour, func(ctx context.Context) {
		refreshVersions(ctx, d)
	})
}

func geoRefreshInterval(cfg *config.Config) time.Duration {
	if cfg.Geo.RefreshInterval == "" {
		return 12 * time.Hour
	}
	if d, err := time.ParseDuration(cfg.Geo.RefreshInterval); err == nil && d > 0 {
		return d
	}
	return 12 * time.Hour
}

func runEvery(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	fn(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

func refreshVersions(ctx context.Context, d *sql.DB) {
	for _, serviceType := range []string{"hysteria2", "xray", "sing-box", "v2ray"} {
		latest, err := panelversion.FetchLatest(serviceType)
		if err != nil {
			log.Printf("fetch %s version: %v", serviceType, err)
			continue
		}
		if _, err := d.ExecContext(ctx, `INSERT INTO versions(service_type,latest_version,source,updated_at)
			VALUES(?,?,?,?)
			ON CONFLICT(service_type) DO UPDATE SET latest_version=?,source=?,updated_at=?`,
			serviceType, latest, "github", time.Now().Unix(), latest, "github", time.Now().Unix()); err != nil {
			log.Printf("store %s version: %v", serviceType, err)
		}
	}
}
