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
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if err := addMissingNodeColumns(tx); err != nil {
		return err
	}
	if err := addMissingGeoCacheColumns(tx); err != nil {
		return err
	}
	if err := addMissingControlColumns(tx); err != nil {
		return err
	}
	if err := addLegacyEnrollmentTokenUseCountGuards(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_nodes_ip_geo_ip ON nodes(ip_geo_ip)`); err != nil {
		return err
	}
	return tx.Commit()
}

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func addMissingNodeColumns(d sqlExecutor) error {
	for _, col := range []struct {
		name string
		def  string
	}{
		{name: "ip_geo_ip", def: "TEXT"},
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

func addMissingGeoCacheColumns(d sqlExecutor) error {
	for _, col := range []struct {
		name string
		def  string
	}{
		{name: "last_attempt_at", def: "INTEGER NOT NULL DEFAULT 0"},
		{name: "failure_count", def: "INTEGER NOT NULL DEFAULT 0"},
		{name: "retry_at", def: "INTEGER NOT NULL DEFAULT 0"},
	} {
		var count int
		if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('ip_geo_cache') WHERE name=?`, col.name).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if _, err := d.Exec(`ALTER TABLE ip_geo_cache ADD COLUMN ` + col.name + ` ` + col.def); err != nil {
			return err
		}
	}
	return nil
}

func addMissingControlColumns(d sqlExecutor) error {
	for _, change := range []struct {
		table string
		name  string
		def   string
	}{
		{table: "users", name: "role", def: "TEXT NOT NULL DEFAULT 'viewer' CHECK(role IN ('viewer','operator','admin'))"},
		{table: "sessions", name: "session_id", def: "TEXT NOT NULL DEFAULT ''"},
		{table: "sessions", name: "reauthenticated_at", def: "INTEGER NOT NULL DEFAULT 0"},
	} {
		var count int
		if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, change.table, change.name).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if _, err := d.Exec(`ALTER TABLE ` + change.table + ` ADD COLUMN ` + change.name + ` ` + change.def); err != nil {
			return err
		}
		if change.table == "users" && change.name == "role" {
			// Before roles, every authenticated Panel user had administrative
			// access. Preserve that capability for an in-place upgrade; new users
			// receive the viewer default from the schema.
			if _, err := d.Exec(`UPDATE users SET role='admin'`); err != nil {
				return err
			}
		}
		if change.table == "sessions" && change.name == "session_id" {
			if _, err := d.Exec(`UPDATE sessions SET session_id='legacy-' || rowid WHERE session_id=''`); err != nil {
				return err
			}
		}
	}
	_, err := d.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_session_id ON sessions(session_id) WHERE session_id <> ''`)
	return err
}

func addLegacyEnrollmentTokenUseCountGuards(d sqlExecutor) error {
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('agent_enrollment_tokens') WHERE name='use_count'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET use_limit=1 WHERE use_limit<>1`); err != nil {
		return err
	}
	// Legacy schemas allowed negative timestamps. They never represented a valid
	// completed enrollment, so normalize them to the unconsumed sentinel before
	// reconciling the legacy counter.
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET consumed_at=0 WHERE consumed_at<0`); err != nil {
		return err
	}
	if _, err := d.Exec(`UPDATE agent_enrollment_tokens SET use_count=CASE WHEN consumed_at>0 THEN 1 ELSE 0 END
		WHERE use_count<>CASE WHEN consumed_at>0 THEN 1 ELSE 0 END`); err != nil {
		return err
	}
	_, err := d.Exec(`
CREATE TRIGGER IF NOT EXISTS agent_enrollment_tokens_legacy_use_count_insert
BEFORE INSERT ON agent_enrollment_tokens
WHEN NEW.use_count NOT IN (0,1) OR NEW.use_count>NEW.use_limit
	OR (NEW.use_count=0 AND NEW.consumed_at<>0) OR (NEW.use_count=1 AND NEW.consumed_at<=0)
BEGIN SELECT RAISE(ABORT, 'invalid enrollment token use count'); END;
CREATE TRIGGER IF NOT EXISTS agent_enrollment_tokens_legacy_use_count_update
BEFORE UPDATE OF use_count ON agent_enrollment_tokens
WHEN NEW.use_count NOT IN (0,1) OR NEW.use_count>NEW.use_limit
	OR (NEW.use_count=0 AND NEW.consumed_at<>0) OR (NEW.use_count=1 AND NEW.consumed_at<=0)
BEGIN SELECT RAISE(ABORT, 'invalid enrollment token use count'); END;
CREATE TRIGGER IF NOT EXISTS agent_enrollment_tokens_legacy_use_count_on_consume
AFTER UPDATE OF consumed_at ON agent_enrollment_tokens
WHEN NEW.consumed_at>0 AND NEW.use_count=0
BEGIN UPDATE agent_enrollment_tokens SET use_count=1 WHERE id=NEW.id; END;`)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY, username TEXT UNIQUE, password_hash TEXT, created_at INTEGER, role TEXT NOT NULL DEFAULT 'viewer' CHECK(role IN ('viewer','operator','admin')));
CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY, session_id TEXT NOT NULL UNIQUE, user_id INTEGER, created_at INTEGER, expires_at INTEGER, reauthenticated_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS nodes(id INTEGER PRIMARY KEY, node_id TEXT UNIQUE, alias TEXT, token TEXT, muted_until INTEGER, last_report_at INTEGER, last_host_json TEXT, last_services_json TEXT, created_at INTEGER, updated_at INTEGER, ip_geo_ip TEXT, ip_location TEXT, ip_country TEXT, ip_region TEXT, ip_city TEXT, ip_geo_updated_at INTEGER);
CREATE TABLE IF NOT EXISTS ip_geo_cache(ip TEXT PRIMARY KEY, location TEXT, country TEXT, region TEXT, city TEXT, updated_at INTEGER NOT NULL, last_attempt_at INTEGER NOT NULL DEFAULT 0, failure_count INTEGER NOT NULL DEFAULT 0, retry_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS tags(id INTEGER PRIMARY KEY, name TEXT UNIQUE);
CREATE TABLE IF NOT EXISTS node_tags(node_id INTEGER, tag_id INTEGER, PRIMARY KEY(node_id, tag_id));
CREATE TABLE IF NOT EXISTS metrics(id INTEGER PRIMARY KEY AUTOINCREMENT, node_id TEXT, ts INTEGER, granularity TEXT, load1 REAL, load5 REAL, load15 REAL, mem_used_pct REAL, disk_used_pct REAL, services_json TEXT);
CREATE INDEX IF NOT EXISTS idx_metrics_node_ts ON metrics(node_id, ts);
CREATE TABLE IF NOT EXISTS alerts(id INTEGER PRIMARY KEY, node_id TEXT, rule TEXT, status TEXT, message TEXT, first_seen_at INTEGER, last_seen_at INTEGER, recovered_at INTEGER, acknowledged_at INTEGER);
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_active ON alerts(node_id, rule) WHERE status='firing';
CREATE TABLE IF NOT EXISTS versions(service_type TEXT PRIMARY KEY, latest_version TEXT, source TEXT, updated_at INTEGER);
CREATE TABLE IF NOT EXISTS agent_identities(
	agent_id TEXT PRIMARY KEY,
	node_id TEXT NOT NULL UNIQUE,
	cert_serial TEXT NOT NULL,
	cert_fingerprint TEXT NOT NULL,
	issued_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	revoked_at INTEGER NOT NULL DEFAULT 0,
	last_connected_at INTEGER NOT NULL DEFAULT 0,
	last_heartbeat_at INTEGER NOT NULL DEFAULT 0,
	last_disconnected_at INTEGER NOT NULL DEFAULT 0,
	disconnect_reason TEXT NOT NULL DEFAULT '',
	agent_version TEXT NOT NULL DEFAULT '',
	os TEXT NOT NULL DEFAULT '',
	arch TEXT NOT NULL DEFAULT '',
	capabilities_json TEXT NOT NULL DEFAULT '[]',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_identity_serial ON agent_identities(cert_serial);
CREATE TABLE IF NOT EXISTS agent_enrollment_tokens(
	id INTEGER PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	created_by_user_id INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	consumed_at INTEGER NOT NULL DEFAULT 0 CHECK(consumed_at >= 0),
	use_limit INTEGER NOT NULL DEFAULT 1 CHECK(use_limit = 1),
	label TEXT NOT NULL DEFAULT ''
);
CREATE TRIGGER IF NOT EXISTS agent_enrollment_tokens_one_use_insert
BEFORE INSERT ON agent_enrollment_tokens WHEN NEW.use_limit <> 1
BEGIN SELECT RAISE(ABORT, 'enrollment tokens are single-use'); END;
CREATE TRIGGER IF NOT EXISTS agent_enrollment_tokens_one_use_update
BEFORE UPDATE OF use_limit ON agent_enrollment_tokens WHEN NEW.use_limit <> 1
BEGIN SELECT RAISE(ABORT, 'enrollment tokens are single-use'); END;
CREATE TRIGGER IF NOT EXISTS agent_enrollment_tokens_consume_once
BEFORE UPDATE OF consumed_at ON agent_enrollment_tokens
WHEN OLD.consumed_at > 0
	OR (OLD.consumed_at = 0 AND NEW.consumed_at <= 0)
	OR (OLD.consumed_at < 0 AND NEW.consumed_at <> 0)
BEGIN SELECT RAISE(ABORT, 'enrollment token already consumed'); END;
CREATE TABLE IF NOT EXISTS agent_commands(
	command_id TEXT PRIMARY KEY,
	agent_id TEXT NOT NULL,
	sequence INTEGER NOT NULL CHECK(sequence > 0),
	control_version TEXT NOT NULL DEFAULT '1' CHECK(control_version = '1'),
	message_type TEXT NOT NULL DEFAULT 'command' CHECK(message_type = 'command'),
	action TEXT NOT NULL CHECK(action IN ('collect_now','reload_config','self_check','upgrade')),
	payload TEXT NOT NULL CHECK(length(payload) <= 65536),
	signature TEXT NOT NULL,
	state TEXT NOT NULL CHECK(state IN ('queued','dispatched','accepted','running','succeeded','failed','expired')),
	issued_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	dispatched_at INTEGER NOT NULL DEFAULT 0,
	accepted_at INTEGER NOT NULL DEFAULT 0,
	started_at INTEGER NOT NULL DEFAULT 0,
	finished_at INTEGER NOT NULL DEFAULT 0,
	attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0),
	result_code TEXT NOT NULL DEFAULT '' CHECK(length(result_code) <= 128),
	result_json TEXT NOT NULL DEFAULT '{}' CHECK(length(result_json) <= 16384),
	created_by_user_id INTEGER NOT NULL DEFAULT 0,
	created_session_id TEXT NOT NULL DEFAULT '',
	UNIQUE(agent_id, sequence)
);
CREATE TRIGGER IF NOT EXISTS agent_commands_result_code_bound_insert
BEFORE INSERT ON agent_commands WHEN length(NEW.result_code) > 128
BEGIN SELECT RAISE(ABORT, 'command result code exceeds limit'); END;
CREATE TRIGGER IF NOT EXISTS agent_commands_result_code_bound_update
BEFORE UPDATE OF result_code ON agent_commands WHEN length(NEW.result_code) > 128
BEGIN SELECT RAISE(ABORT, 'command result code exceeds limit'); END;
CREATE TRIGGER IF NOT EXISTS agent_commands_session_reference_insert
BEFORE INSERT ON agent_commands
WHEN NEW.created_session_id <> '' AND NOT EXISTS (SELECT 1 FROM sessions WHERE session_id=NEW.created_session_id)
BEGIN SELECT RAISE(ABORT, 'unknown command session reference'); END;
CREATE TRIGGER IF NOT EXISTS agent_commands_session_reference_update
BEFORE UPDATE OF created_session_id ON agent_commands
WHEN NEW.created_session_id <> '' AND NOT EXISTS (SELECT 1 FROM sessions WHERE session_id=NEW.created_session_id)
BEGIN SELECT RAISE(ABORT, 'unknown command session reference'); END;
CREATE INDEX IF NOT EXISTS idx_agent_commands_delivery ON agent_commands(agent_id,state,expires_at);
CREATE INDEX IF NOT EXISTS idx_agent_commands_expiry ON agent_commands(state,expires_at);
CREATE TABLE IF NOT EXISTS agent_command_events(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	command_id TEXT NOT NULL,
	actor_id INTEGER NOT NULL,
	session_id TEXT NOT NULL,
	from_state TEXT NOT NULL CHECK(from_state IN ('','queued','dispatched','accepted','running','succeeded','failed','expired')),
	to_state TEXT NOT NULL CHECK(to_state IN ('queued','dispatched','accepted','running','succeeded','failed','expired')),
	created_at INTEGER NOT NULL,
	reason_code TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS agent_command_events_session_reference_insert
BEFORE INSERT ON agent_command_events
WHEN NEW.session_id <> '' AND NOT EXISTS (SELECT 1 FROM sessions WHERE session_id=NEW.session_id)
BEGIN SELECT RAISE(ABORT, 'unknown command event session reference'); END;
CREATE TRIGGER IF NOT EXISTS agent_command_events_no_update
BEFORE UPDATE ON agent_command_events BEGIN SELECT RAISE(ABORT, 'agent command events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS agent_command_events_no_delete
BEFORE DELETE ON agent_command_events BEGIN SELECT RAISE(ABORT, 'agent command events are append-only'); END;
CREATE TABLE IF NOT EXISTS agent_releases(
	id INTEGER PRIMARY KEY,
	manifest_json TEXT NOT NULL CHECK(length(manifest_json) <= 65536),
	signature TEXT NOT NULL,
	version TEXT NOT NULL,
	os TEXT NOT NULL,
	arch TEXT NOT NULL,
	size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
	sha256 TEXT NOT NULL,
	imported_by_user_id INTEGER NOT NULL,
	imported_at INTEGER NOT NULL,
	UNIQUE(version, os, arch)
);
`
