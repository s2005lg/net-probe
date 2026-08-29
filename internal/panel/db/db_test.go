package db

import (
	"database/sql"
	"path/filepath"
	"strings"
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
	for _, column := range []string{"last_attempt_at", "failure_count", "retry_at"} {
		if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('ip_geo_cache') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("ip_geo_cache.%s count=%d err=%v", column, count, err)
		}
	}
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_nodes_ip_geo_ip'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idx_nodes_ip_geo_ip count=%d err=%v", count, err)
	}
}

func TestMigratePreservesPreExistingRowsAndAddsGeoShape(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	legacySchema := `
CREATE TABLE nodes(id INTEGER PRIMARY KEY, node_id TEXT UNIQUE, alias TEXT, token TEXT, muted_until INTEGER, last_report_at INTEGER, last_host_json TEXT, last_services_json TEXT, created_at INTEGER, updated_at INTEGER);
CREATE TABLE ip_geo_cache(ip TEXT PRIMARY KEY, location TEXT, country TEXT, region TEXT, city TEXT, updated_at INTEGER NOT NULL);`
	if _, err := d.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO nodes(node_id,alias,last_host_json,last_services_json,created_at,updated_at) VALUES('legacy','kept','{}','[]',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO ip_geo_cache(ip,location,country,region,city,updated_at) VALUES('8.8.8.8','旧位置','美国','','',123)`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	var alias, geoIP string
	if err := d.QueryRow(`SELECT alias, COALESCE(ip_geo_ip,'') FROM nodes WHERE node_id='legacy'`).Scan(&alias, &geoIP); err != nil {
		t.Fatal(err)
	}
	if alias != "kept" || geoIP != "" {
		t.Fatalf("legacy node alias=%q geoIP=%q", alias, geoIP)
	}
	var location string
	var updatedAt, lastAttemptAt, failureCount, retryAt int64
	if err := d.QueryRow(`SELECT location,updated_at,last_attempt_at,failure_count,retry_at FROM ip_geo_cache WHERE ip='8.8.8.8'`).Scan(&location, &updatedAt, &lastAttemptAt, &failureCount, &retryAt); err != nil {
		t.Fatal(err)
	}
	if location != "旧位置" || updatedAt != 123 || lastAttemptAt != 0 || failureCount != 0 || retryAt != 0 {
		t.Fatalf("legacy cache location=%q updated=%d attempt=%d failures=%d retry=%d", location, updatedAt, lastAttemptAt, failureCount, retryAt)
	}
	assertGeoIndexUsable(t, d)
}

func assertGeoIndexUsable(t *testing.T, d *sql.DB) {
	t.Helper()
	rows, err := d.Query(`EXPLAIN QUERY PLAN SELECT node_id FROM nodes WHERE ip_geo_ip='8.8.8.8'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id, parent, unused int
	var detail string
	if !rows.Next() {
		t.Fatal("query plan is empty")
	}
	if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "idx_nodes_ip_geo_ip") {
		t.Fatalf("query plan does not use Geo index: %q", detail)
	}
}
