package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleReport(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	body := `{"schema_version":"1","agent_version":"v0.1.1","node_id":"n1","collected_at":"2026-08-18T00:00:00Z","host":{"hostname":"h","load1":0.1,"load5":0.2,"load15":0.3,"mem_used_pct":12.5,"disk_used_pct":13.5},"services":[]}`
	req := httptest.NewRequest("POST", "/api/v1/agents/report", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rr := httptest.NewRecorder()
	s.handleReport(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"ack":true`) {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleReportUnauthorized(t *testing.T) {
	_, cfg := openTestDB(t)
	s := New(nil, cfg)
	req := httptest.NewRequest("POST", "/api/v1/agents/report", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong")
	rr := httptest.NewRecorder()
	s.handleReport(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rr.Code)
	}
}
