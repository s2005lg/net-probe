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
	if err := Migrate(d); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var count int
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='nodes'`).Scan(&count); err != nil {
		t.Fatalf("query nodes table: %v", err)
	}
	if count != 1 {
		t.Fatalf("nodes table missing: count=%d", count)
	}
	for _, column := range []string{"ip_geo_ip", "ip_location", "ip_country", "ip_region", "ip_city", "ip_geo_updated_at"} {
		if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('nodes') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("nodes.%s count=%d err=%v", column, count, err)
		}
	}
	var cacheTable int
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='ip_geo_cache'`).Scan(&cacheTable); err != nil || cacheTable != 1 {
		t.Fatalf("ip_geo_cache count=%d err=%v", cacheTable, err)
	}
}
