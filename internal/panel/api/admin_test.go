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
