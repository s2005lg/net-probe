package db

import (
	"path/filepath"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if err := Migrate(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var count int
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='nodes'`).Scan(&count); err != nil {
		t.Fatalf("query nodes table: %v", err)
	}
	if count != 1 {
		t.Fatalf("nodes table missing: count=%d", count)
	}
}
