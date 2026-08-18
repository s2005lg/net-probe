package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func insertTestNode(t *testing.T, d *sql.DB, nodeID, alias, hostJSON, svcJSON string, lastAt int64) {
	t.Helper()
	_, err := d.Exec(`INSERT INTO nodes(node_id,alias,last_report_at,last_host_json,last_services_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`, nodeID, alias, lastAt, hostJSON, svcJSON, lastAt, lastAt)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}
}

func TestNodesList(t *testing.T) {
	d, cfg := openTestDB(t)
	now := time.Now().Unix()
	insertTestNode(t, d, "n1", "edge", `{"hostname":"h1"}`, `[{"type":"xray"}]`, now)
	s := New(d, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes", nil)
	rr := httptest.NewRecorder()
	s.handleNodes(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0]["node_id"] != "n1" {
		t.Fatalf("nodes=%+v", out)
	}
}

func TestNodeDetailPatchDelete(t *testing.T) {
	d, cfg := openTestDB(t)
	now := time.Now().Unix()
	insertTestNode(t, d, "n1", "edge", `{"hostname":"h1"}`, `[{"type":"xray"}]`, now)
	s := New(d, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes/n1", nil)
	req.SetPathValue("id", "n1")
	rr := httptest.NewRecorder()
	s.handleNodeDetail(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail code=%d body=%s", rr.Code, rr.Body.String())
	}

	body := `{"alias":"renamed","muted_until":123456}`
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/nodes/n1", strings.NewReader(body))
	req.SetPathValue("id", "n1")
	rr = httptest.NewRecorder()
	s.handleNodePatch(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch code=%d body=%s", rr.Code, rr.Body.String())
	}
	var alias, nodeID string
	var mutedUntil int64
	if err := d.QueryRow(`SELECT node_id,alias,muted_until FROM nodes WHERE node_id='n1'`).Scan(&nodeID, &alias, &mutedUntil); err != nil {
		t.Fatalf("query node: %v", err)
	}
	if alias != "renamed" || mutedUntil != 123456 {
		t.Fatalf("alias=%q muted=%d", alias, mutedUntil)
	}

	_, _ = d.Exec(`INSERT INTO metrics(node_id,ts,granularity) VALUES('n1',1,'raw')`)
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/nodes/n1", nil)
	req.SetPathValue("id", "n1")
	rr = httptest.NewRecorder()
	s.handleNodeDelete(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete code=%d body=%s", rr.Code, rr.Body.String())
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM nodes WHERE node_id='n1'`).Scan(&count); err != nil {
		t.Fatalf("query nodes after delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("node still exists")
	}
	if err := d.QueryRow(`SELECT count(*) FROM metrics WHERE node_id='n1'`).Scan(&count); err != nil {
		t.Fatalf("query metrics after delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("metrics still exist")
	}
}

func TestNodeMetrics(t *testing.T) {
	d, cfg := openTestDB(t)
	now := time.Now().Unix()
	insertTestNode(t, d, "n1", "edge", `{"hostname":"h1"}`, `[{"type":"xray"}]`, now)
	if _, err := d.Exec(`INSERT INTO metrics(node_id,ts,granularity,load1,load5,load15,mem_used_pct,disk_used_pct,services_json)
		VALUES('n1',?,'raw',0.1,0.2,0.3,12,20,'[]')`, now); err != nil {
		t.Fatalf("insert metric: %v", err)
	}
	s := New(d, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes/n1/metrics?granularity=raw", nil)
	req.SetPathValue("id", "n1")
	rr := httptest.NewRecorder()
	s.handleNodeMetrics(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0]["granularity"] != "raw" {
		t.Fatalf("metrics=%+v", out)
	}
}

func TestVersionsList(t *testing.T) {
	d, cfg := openTestDB(t)
	if _, err := d.Exec(`INSERT INTO versions(service_type,latest_version,source,updated_at) VALUES('hysteria2','2.12.1','github',?)`, time.Now().Unix()); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	s := New(d, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/versions", nil)
	rr := httptest.NewRecorder()
	s.handleVersions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0]["service_type"] != "hysteria2" || out[0]["latest_version"] != "2.12.1" {
		t.Fatalf("versions=%+v", out)
	}
}

func TestOverview(t *testing.T) {
	d, cfg := openTestDB(t)
	now := time.Now().Unix()
	insertTestNode(t, d, "n1", "a", `{"hostname":"h1"}`, `[{"type":"xray"},{"type":"xray"}]`, now)
	insertTestNode(t, d, "n2", "b", `{"hostname":"h2"}`, `[{"type":"hysteria2"}]`, now-600)
	if _, err := d.Exec(`INSERT INTO alerts(node_id,rule,status,message,first_seen_at,last_seen_at)
		VALUES('n2','node_offline','firing','offline',?,?)`, now, now); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	s := New(d, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview", nil)
	rr := httptest.NewRecorder()
	s.handleOverview(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["nodes_total"] != float64(2) || out["nodes_online"] != float64(1) ||
		out["alerts_active"] != float64(1) || out["services_total"] != float64(3) {
		t.Fatalf("overview=%+v", out)
	}
}

func TestAlertsAndAck(t *testing.T) {
	d, cfg := openTestDB(t)
	now := time.Now().Unix()
	if _, err := d.Exec(`INSERT INTO alerts(node_id,rule,status,message,first_seen_at,last_seen_at)
		VALUES('n1','node_offline','firing','offline',?,?)`, now, now); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	var id int64
	if err := d.QueryRow(`SELECT id FROM alerts WHERE node_id='n1'`).Scan(&id); err != nil {
		t.Fatalf("query alert id: %v", err)
	}
	s := New(d, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/alerts", nil)
	rr := httptest.NewRecorder()
	s.handleAlerts(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0]["status"] != "firing" {
		t.Fatalf("alerts=%+v", out)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/alerts/1/ack", nil)
	req.SetPathValue("id", "1")
	rr = httptest.NewRecorder()
	s.handleAlertAck(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("ack code=%d body=%s", rr.Code, rr.Body.String())
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM alerts WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatalf("query acked: %v", err)
	}
	if status != "acknowledged" {
		t.Fatalf("status=%q", status)
	}
}

func TestVersionPatch(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/versions/xray", strings.NewReader(`{"latest_version":"25.1.1"}`))
	req.SetPathValue("service_type", "xray")
	rr := httptest.NewRecorder()
	s.handleVersionPatch(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var latest, source string
	if err := d.QueryRow(`SELECT latest_version, source FROM versions WHERE service_type='xray'`).Scan(&latest, &source); err != nil {
		t.Fatalf("query version: %v", err)
	}
	if latest != "25.1.1" || source != "manual" {
		t.Fatalf("latest=%q source=%q", latest, source)
	}
}

func TestSettings(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	rr := httptest.NewRecorder()
	s.handleSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["node_timeout"] != "3m" {
		t.Fatalf("settings=%+v", out)
	}
}
