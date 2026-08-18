package api

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/s2005lg/net-probe/internal/panel/config"
	"github.com/s2005lg/net-probe/internal/panel/db"
)

func openTestDB(t *testing.T) (*sql.DB, *config.Config) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	cfg := config.Default()
	cfg.Agent.Token = "tok"
	return d, cfg
}
