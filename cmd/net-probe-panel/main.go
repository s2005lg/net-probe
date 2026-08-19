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

	certPath := filepath.Join(cfg.DataDir, "cert.pem")
	keyPath := filepath.Join(cfg.DataDir, "key.pem")
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		if err := api.GenerateSelfSigned(certPath, keyPath); err != nil {
			log.Fatalf("generate self-signed cert: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startBackground(ctx, d, cfg)

	server := api.New(d, cfg)
	log.Printf("net-probe-panel listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServeTLS(cfg.ListenAddr, certPath, keyPath, server.Routes()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func startBackground(ctx context.Context, d *sql.DB, cfg *config.Config) {
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
	go runEvery(ctx, 24*time.Hour, func(ctx context.Context) {
		refreshVersions(ctx, d)
	})
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
