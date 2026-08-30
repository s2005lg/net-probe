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

func TestMigrateControlPlaneSchema(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(d); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"agent_identities", "agent_enrollment_tokens", "agent_commands", "agent_command_events", "agent_releases"} {
		var count int
		if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s missing", table)
		}
	}

	for _, column := range []string{"role"} {
		var count int
		if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('users') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("users.%s count=%d err=%v", column, count, err)
		}
	}
	for _, column := range []string{"reauthenticated_at"} {
		var count int
		if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('sessions') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("sessions.%s count=%d err=%v", column, count, err)
		}
	}

	if _, err := d.Exec(`INSERT INTO agent_commands(command_id,agent_id,sequence,action,payload,signature,state,issued_at,expires_at,result_json)
		VALUES('cmd-1','agent-1',1,'collect_now','{}','sig','queued',1,2,'{}')`); err != nil {
		t.Fatalf("insert valid command: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO agent_commands(command_id,agent_id,sequence,action,payload,signature,state,issued_at,expires_at,result_json)
		VALUES('cmd-2','agent-1',1,'collect_now','{}','sig','queued',1,2,'{}')`); err == nil {
		t.Fatal("duplicate sequence accepted")
	}
	if _, err := d.Exec(`INSERT INTO agent_commands(command_id,agent_id,sequence,action,payload,signature,state,issued_at,expires_at,result_json)
		VALUES('cmd-3','agent-1',2,'shell','{}','sig','queued',1,2,'{}')`); err == nil {
		t.Fatal("invalid action accepted")
	}
	if _, err := d.Exec(`INSERT INTO agent_command_events(command_id,actor_id,session_id,from_state,to_state,created_at,reason_code)
		VALUES('cmd-1',1,'session','queued','dispatched',3,'delivered')`); err != nil {
		t.Fatalf("insert command event: %v", err)
	}
	if _, err := d.Exec(`UPDATE agent_command_events SET reason_code='changed' WHERE command_id='cmd-1'`); err == nil {
		t.Fatal("command event update accepted")
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

func TestMigratePreservesLegacyUsersAndSessions(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "legacy-users.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`
CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT UNIQUE, password_hash TEXT, created_at INTEGER);
CREATE TABLE sessions(token TEXT PRIMARY KEY, user_id INTEGER, created_at INTEGER, expires_at INTEGER);
INSERT INTO users(id,username,password_hash,created_at) VALUES(9,'legacy','hash',1);
INSERT INTO sessions(token,user_id,created_at,expires_at) VALUES('legacy-session',9,1,2);`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	var username, role string
	if err := d.QueryRow(`SELECT username,role FROM users WHERE id=9`).Scan(&username, &role); err != nil {
		t.Fatal(err)
	}
	if username != "legacy" || role != "admin" {
		t.Fatalf("legacy user username=%q role=%q", username, role)
	}
	var userID, reauthenticatedAt int64
	var sessionID string
	if err := d.QueryRow(`SELECT user_id,session_id,reauthenticated_at FROM sessions WHERE token='legacy-session'`).Scan(&userID, &sessionID, &reauthenticatedAt); err != nil {
		t.Fatal(err)
	}
	if userID != 9 || sessionID == "" || sessionID == "legacy-session" || reauthenticatedAt != 0 {
		t.Fatalf("legacy session user=%d session_id=%q reauthenticated_at=%d", userID, sessionID, reauthenticatedAt)
	}
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
