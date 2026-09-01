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
	const rawBearerToken = "panel-session-bearer"
	if _, err := d.Exec(`INSERT INTO sessions(token,session_id,user_id,created_at,expires_at) VALUES(?,?,?,?,?)`, rawBearerToken, "audit-session-ref", 1, 1, 2); err != nil {
		t.Fatalf("insert audit session: %v", err)
	}

	if _, err := d.Exec(`INSERT INTO agent_commands(command_id,agent_id,sequence,action,payload,signature,state,issued_at,expires_at,result_json,created_session_id)
		VALUES('cmd-1','agent-1',1,'collect_now','{}','sig','queued',1,2,'{}','audit-session-ref')`); err != nil {
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
		VALUES('cmd-1',1,'audit-session-ref','queued','dispatched',3,'delivered')`); err != nil {
		t.Fatalf("insert command event: %v", err)
	}
	if _, err := d.Exec(`UPDATE agent_command_events SET reason_code='changed' WHERE command_id='cmd-1'`); err == nil {
		t.Fatal("command event update accepted")
	}
	if _, err := d.Exec(`INSERT INTO agent_enrollment_tokens(token_hash,created_by_user_id,created_at,expires_at,use_limit,label)
		VALUES('token-1',1,1,2,2,'too-many-uses')`); err == nil {
		t.Fatal("multi-use enrollment token accepted")
	}
	if _, err := d.Exec(`INSERT INTO agent_enrollment_tokens(token_hash,created_by_user_id,created_at,expires_at,label)
		VALUES('token-2',1,1,2,'one-use')`); err != nil {
		t.Fatalf("insert one-use enrollment token: %v", err)
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET consumed_at=3 WHERE token_hash='token-2'`); err != nil {
		t.Fatalf("consume enrollment token: %v", err)
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET consumed_at=4 WHERE token_hash='token-2'`); err == nil {
		t.Fatal("enrollment token consumed twice")
	}
	if _, err := d.Exec(`INSERT INTO agent_enrollment_tokens(token_hash,created_by_user_id,created_at,expires_at,consumed_at,label)
		VALUES('token-negative',1,1,2,-1,'invalid-timestamp')`); err == nil {
		t.Fatal("negative enrollment consumption timestamp accepted")
	}
	if _, err := d.Exec(`INSERT INTO agent_commands(command_id,agent_id,sequence,action,payload,signature,state,issued_at,expires_at,result_code,result_json)
		VALUES('cmd-4','agent-1',4,'collect_now','{}','sig','queued',1,2,?,'{}')`, strings.Repeat("x", 129)); err == nil {
		t.Fatal("oversized command result code accepted")
	}
	if _, err := d.Exec(`INSERT INTO agent_commands(command_id,agent_id,sequence,action,payload,signature,state,issued_at,expires_at,result_json,created_session_id)
		VALUES('cmd-5','agent-1',5,'collect_now','{}','sig','queued',1,2,'{}',?)`, rawBearerToken); err == nil {
		t.Fatal("raw bearer command audit reference accepted")
	}
	if _, err := d.Exec(`INSERT INTO agent_command_events(command_id,actor_id,session_id,from_state,to_state,created_at,reason_code)
		VALUES('cmd-1',1,?,'queued','dispatched',4,'delivered')`, rawBearerToken); err == nil {
		t.Fatal("raw bearer event audit reference accepted")
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

func TestMigrateHardensLegacyEnrollmentTokenUseCount(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "legacy-enrollment.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`
CREATE TABLE agent_enrollment_tokens(
	id INTEGER PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	created_by_user_id INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	consumed_at INTEGER NOT NULL DEFAULT 0,
	use_limit INTEGER NOT NULL DEFAULT 1 CHECK(use_limit > 0),
	use_count INTEGER NOT NULL DEFAULT 0 CHECK(use_count >= 0),
	label TEXT NOT NULL DEFAULT ''
);
INSERT INTO agent_enrollment_tokens(token_hash,created_by_user_id,created_at,expires_at,consumed_at,use_limit,use_count,label)
VALUES('unconsumed',1,1,2,0,1,0,'preserve'),('consumed',1,1,2,3,2,2,'normalize'),('negative',1,1,2,-1,1,0,'normalize-negative');
CREATE TRIGGER agent_enrollment_tokens_one_use_insert
BEFORE INSERT ON agent_enrollment_tokens WHEN NEW.use_limit <> 1
BEGIN SELECT RAISE(ABORT, 'enrollment tokens are single-use'); END;
CREATE TRIGGER agent_enrollment_tokens_one_use_update
BEFORE UPDATE OF use_limit ON agent_enrollment_tokens WHEN NEW.use_limit <> 1
BEGIN SELECT RAISE(ABORT, 'enrollment tokens are single-use'); END;
CREATE TRIGGER agent_enrollment_tokens_consume_once
BEFORE UPDATE OF consumed_at ON agent_enrollment_tokens
WHEN OLD.consumed_at <> 0 OR NEW.consumed_at <= 0
BEGIN SELECT RAISE(ABORT, 'enrollment token already consumed'); END;
CREATE TRIGGER agent_enrollment_tokens_legacy_use_count_insert
BEFORE INSERT ON agent_enrollment_tokens
WHEN NEW.use_count NOT IN (0,1) OR NEW.use_count>NEW.use_limit
	OR (NEW.use_count=0 AND NEW.consumed_at<>0) OR (NEW.use_count=1 AND NEW.consumed_at<=0)
BEGIN SELECT RAISE(ABORT, 'invalid enrollment token use count'); END;
CREATE TRIGGER agent_enrollment_tokens_legacy_use_count_update
BEFORE UPDATE OF use_count ON agent_enrollment_tokens
WHEN NEW.use_count NOT IN (0,1) OR NEW.use_count>NEW.use_limit
	OR (NEW.use_count=0 AND NEW.consumed_at<>0) OR (NEW.use_count=1 AND NEW.consumed_at<=0)
BEGIN SELECT RAISE(ABORT, 'invalid enrollment token use count'); END;
CREATE TRIGGER agent_enrollment_tokens_legacy_use_count_on_consume
AFTER UPDATE OF consumed_at ON agent_enrollment_tokens
WHEN NEW.consumed_at>0 AND NEW.use_count=0
BEGIN UPDATE agent_enrollment_tokens SET use_count=1 WHERE id=NEW.id; END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET consumed_at=0 WHERE token_hash='negative'`); err == nil {
		t.Fatal("legacy consume-once trigger did not reject negative timestamp normalization")
	}
	if err := Migrate(d); err != nil {
		t.Fatal(err)
	}

	var useLimit, useCount, consumedAt int64
	var label string
	if err := d.QueryRow(`SELECT use_limit,use_count,consumed_at,label FROM agent_enrollment_tokens WHERE token_hash='unconsumed'`).Scan(&useLimit, &useCount, &consumedAt, &label); err != nil {
		t.Fatal(err)
	}
	if useLimit != 1 || useCount != 0 || consumedAt != 0 || label != "preserve" {
		t.Fatalf("unconsumed token limit=%d count=%d consumed=%d label=%q", useLimit, useCount, consumedAt, label)
	}
	if err := d.QueryRow(`SELECT use_limit,use_count,consumed_at FROM agent_enrollment_tokens WHERE token_hash='consumed'`).Scan(&useLimit, &useCount, &consumedAt); err != nil {
		t.Fatal(err)
	}
	if useLimit != 1 || useCount != 1 || consumedAt != 3 {
		t.Fatalf("consumed token limit=%d count=%d consumed=%d", useLimit, useCount, consumedAt)
	}
	if err := d.QueryRow(`SELECT use_limit,use_count,consumed_at FROM agent_enrollment_tokens WHERE token_hash='negative'`).Scan(&useLimit, &useCount, &consumedAt); err != nil {
		t.Fatal(err)
	}
	if useLimit != 1 || useCount != 0 || consumedAt != 0 {
		t.Fatalf("negative token limit=%d count=%d consumed=%d", useLimit, useCount, consumedAt)
	}

	if _, err := d.Exec(`INSERT INTO agent_enrollment_tokens(token_hash,created_by_user_id,created_at,expires_at,use_limit,use_count,label)
		VALUES('too-many',1,1,2,1,2,'reject')`); err == nil {
		t.Fatal("legacy use_count above one accepted")
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET use_count=2 WHERE token_hash='unconsumed'`); err == nil {
		t.Fatal("legacy use_count update above one accepted")
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET use_count=1 WHERE token_hash='unconsumed'`); err == nil {
		t.Fatal("legacy use_count consumed without timestamp")
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET consumed_at=4 WHERE token_hash='unconsumed'`); err != nil {
		t.Fatalf("consume legacy token: %v", err)
	}
	if err := d.QueryRow(`SELECT use_count FROM agent_enrollment_tokens WHERE token_hash='unconsumed'`).Scan(&useCount); err != nil {
		t.Fatal(err)
	}
	if useCount != 1 {
		t.Fatalf("legacy consumption count=%d", useCount)
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET consumed_at=5 WHERE token_hash='unconsumed'`); err == nil {
		t.Fatal("legacy token consumed twice")
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET consumed_at=6 WHERE token_hash='negative'`); err != nil {
		t.Fatalf("consume normalized negative token: %v", err)
	}
	if err := d.QueryRow(`SELECT use_count FROM agent_enrollment_tokens WHERE token_hash='negative'`).Scan(&useCount); err != nil {
		t.Fatal(err)
	}
	if useCount != 1 {
		t.Fatalf("normalized negative token count=%d", useCount)
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET consumed_at=7 WHERE token_hash='negative'`); err == nil {
		t.Fatal("normalized negative token consumed twice")
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("second migrate: %v", err)
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
