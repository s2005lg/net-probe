package db

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
}

func Migrate(d *sql.DB) error {
	if _, err := d.Exec(schema); err != nil {
		return err
	}
	return addMissingNodeColumns(d)
}

func addMissingNodeColumns(d *sql.DB) error {
	for _, col := range []struct {
		name string
		def  string
	}{
		{name: "ip_location", def: "TEXT"},
		{name: "ip_country", def: "TEXT"},
		{name: "ip_region", def: "TEXT"},
		{name: "ip_city", def: "TEXT"},
		{name: "ip_geo_updated_at", def: "INTEGER"},
	} {
		var count int
		if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('nodes') WHERE name=?`, col.name).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if _, err := d.Exec(`ALTER TABLE nodes ADD COLUMN ` + col.name + ` ` + col.def); err != nil {
			return err
		}
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY, username TEXT UNIQUE, password_hash TEXT, created_at INTEGER);
CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY, user_id INTEGER, created_at INTEGER, expires_at INTEGER);
CREATE TABLE IF NOT EXISTS nodes(id INTEGER PRIMARY KEY, node_id TEXT UNIQUE, alias TEXT, token TEXT, muted_until INTEGER, last_report_at INTEGER, last_host_json TEXT, last_services_json TEXT, created_at INTEGER, updated_at INTEGER, ip_location TEXT, ip_country TEXT, ip_region TEXT, ip_city TEXT, ip_geo_updated_at INTEGER);
CREATE TABLE IF NOT EXISTS tags(id INTEGER PRIMARY KEY, name TEXT UNIQUE);
CREATE TABLE IF NOT EXISTS node_tags(node_id INTEGER, tag_id INTEGER, PRIMARY KEY(node_id, tag_id));
CREATE TABLE IF NOT EXISTS metrics(id INTEGER PRIMARY KEY AUTOINCREMENT, node_id TEXT, ts INTEGER, granularity TEXT, load1 REAL, load5 REAL, load15 REAL, mem_used_pct REAL, disk_used_pct REAL, services_json TEXT);
CREATE INDEX IF NOT EXISTS idx_metrics_node_ts ON metrics(node_id, ts);
CREATE TABLE IF NOT EXISTS alerts(id INTEGER PRIMARY KEY, node_id TEXT, rule TEXT, status TEXT, message TEXT, first_seen_at INTEGER, last_seen_at INTEGER, recovered_at INTEGER, acknowledged_at INTEGER);
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_active ON alerts(node_id, rule) WHERE status='firing';
CREATE TABLE IF NOT EXISTS versions(service_type TEXT PRIMARY KEY, latest_version TEXT, source TEXT, updated_at INTEGER);
`
