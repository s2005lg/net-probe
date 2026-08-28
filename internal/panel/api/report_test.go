package api

import (
	"encoding/json"
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

func TestHandleExtendedReport(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	body := `{"schema_version":"1","agent_version":"v0.1.1","node_id":"n1","collected_at":"2026-08-18T00:00:00Z","host":{"hostname":"h","load1":0.1,"load5":0.2,"load15":0.3,"mem_used_pct":12.5,"disk_used_pct":13.5},"services":[{"type":"xray","protocols":{"state":"ok","items":["vless"],"source":"config"},"capabilities":{"traffic":{"support":"supported","source":"native_api"},"online_clients":{"support":"supported","source":"native_api"}},"telemetry":{"traffic":{"state":"ok","tx_bytes":0,"rx_bytes":0},"online_clients":{"state":"ok","value":0}}}]}`
	req := httptest.NewRequest("POST", "/api/v1/agents/report", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rr := httptest.NewRecorder()
	s.handleReport(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	var stored string
	if err := d.QueryRow(`SELECT last_services_json FROM nodes WHERE node_id=?`, "n1").Scan(&stored); err != nil {
		t.Fatalf("read stored services: %v", err)
	}
	var services []map[string]any
	if err := json.Unmarshal([]byte(stored), &services); err != nil {
		t.Fatalf("decode stored services: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("services=%v", services)
	}
	service := services[0]
	protocols, _ := service["protocols"].(map[string]any)
	items, _ := protocols["items"].([]any)
	if protocols["state"] != "ok" || protocols["source"] != "config" || len(items) != 1 || items[0] != "vless" {
		t.Fatalf("protocols=%v", protocols)
	}
	capabilities, _ := service["capabilities"].(map[string]any)
	trafficCapability, _ := capabilities["traffic"].(map[string]any)
	onlineClientsCapability, _ := capabilities["online_clients"].(map[string]any)
	if trafficCapability["support"] != "supported" || trafficCapability["source"] != "native_api" || onlineClientsCapability["support"] != "supported" || onlineClientsCapability["source"] != "native_api" {
		t.Fatalf("capabilities=%v", capabilities)
	}
	telemetry, _ := service["telemetry"].(map[string]any)
	traffic, _ := telemetry["traffic"].(map[string]any)
	onlineClients, _ := telemetry["online_clients"].(map[string]any)
	if traffic["state"] != "ok" || onlineClients["state"] != "ok" || traffic["tx_bytes"] != float64(0) || traffic["rx_bytes"] != float64(0) || onlineClients["value"] != float64(0) {
		t.Fatalf("telemetry=%v", telemetry)
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
