package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/geo"
	"github.com/s2005lg/net-probe/internal/report"
)

type fakeGeoObserver struct {
	nodeID string
	host   report.Host
	err    error
}

func (o *fakeGeoObserver) ObserveNode(_ context.Context, nodeID string, host report.Host) error {
	o.nodeID, o.host = nodeID, host
	return o.err
}

type blockingGeoProvider struct {
	started  chan struct{}
	release  chan struct{}
	startOne sync.Once
}

func (p *blockingGeoProvider) Lookup(ctx context.Context, _ string) (geo.Location, error) {
	p.startOne.Do(func() { close(p.started) })
	select {
	case <-p.release:
		return geo.Location{Country: "美国", City: "山景城"}, nil
	case <-ctx.Done():
		return geo.Location{}, ctx.Err()
	}
}

func TestHandleReport(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	body := `{"schema_version":"1","agent_version":"v0.1.1","node_id":"n1","collected_at":"2026-08-18T00:00:00Z","host":{"hostname":"h","load1":0.1,"load5":0.2,"load15":0.3,"mem_used_pct":12.5,"disk_used_pct":13.5},"services":[]}`
	req := httptest.NewRequest("POST", "/api/v1/agents/report", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleReport(rr, req, AgentIdentity{NodeID: "n1"})
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"ack":true`) {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleReportObservesEgressIP(t *testing.T) {
	d, cfg := openTestDB(t)
	observer := &fakeGeoObserver{}
	s := New(d, cfg, observer)
	body := `{"schema_version":"1","node_id":"n1","host":{"egress_ipv4":"8.8.8.8"},"services":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/report", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleReport(rr, req, AgentIdentity{NodeID: "n1"})
	if rr.Code != http.StatusOK || observer.nodeID != "n1" || observer.host.EgressIPv4 != "8.8.8.8" {
		t.Fatalf("code=%d observer=%+v", rr.Code, observer)
	}
}

func TestHandleReportObserverFailureStillAcknowledges(t *testing.T) {
	d, cfg := openTestDB(t)
	observer := &fakeGeoObserver{err: errors.New("provider unavailable")}
	s := New(d, cfg, observer)
	body := `{"schema_version":"1","node_id":"n1","host":{"egress_ipv4":"8.8.8.8"},"services":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/report", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleReport(rr, req, AgentIdentity{NodeID: "n1"})

	var storedHost string
	if err := d.QueryRow(`SELECT last_host_json FROM nodes WHERE node_id='n1'`).Scan(&storedHost); err != nil {
		t.Fatalf("read stored report: %v", err)
	}
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ack":true`) || !strings.Contains(storedHost, `"egress_ipv4":"8.8.8.8"`) {
		t.Fatalf("code=%d body=%s host=%s", rr.Code, rr.Body.String(), storedHost)
	}
}

func TestHandleReportFailedNodePersistenceSkipsObserver(t *testing.T) {
	d, cfg := openTestDB(t)
	if _, err := d.Exec(`DROP TABLE nodes`); err != nil {
		t.Fatal(err)
	}
	observer := &fakeGeoObserver{}
	s := New(d, cfg, observer)
	body := `{"schema_version":"1","node_id":"n1","host":{"egress_ipv4":"8.8.8.8"},"services":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/report", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleReport(rr, req, AgentIdentity{NodeID: "n1"})
	if observer.nodeID != "" {
		t.Fatalf("observer called after failed node persistence: %+v", observer)
	}
}

func TestReportToGeoIntegration(t *testing.T) {
	d, cfg := openTestDB(t)
	provider := &blockingGeoProvider{started: make(chan struct{}), release: make(chan struct{})}
	refresher := geo.NewRefresher(d, provider, 12*time.Hour, nil)
	ctx, cancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		refresher.Run(ctx)
		close(workerDone)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-workerDone:
		case <-time.After(time.Second):
			t.Error("geolocation worker did not stop")
		}
	})

	s := New(d, cfg, refresher)
	body := `{"schema_version":"1","node_id":"n1","host":{"egress_ipv4":"8.8.8.8"},"services":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/report", strings.NewReader(body))
	rr := httptest.NewRecorder()
	ackDone := make(chan struct{})
	go func() {
		s.handleReport(rr, req, AgentIdentity{NodeID: "n1"})
		close(ackDone)
	}()

	select {
	case <-provider.started:
	case <-time.After(time.Second):
		close(provider.release)
		t.Fatal("provider lookup did not start")
	}
	select {
	case <-ackDone:
	case <-time.After(time.Second):
		close(provider.release)
		t.Fatal("report ACK waited for provider lookup")
	}
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ack":true`) {
		close(provider.release)
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	close(provider.release)

	deadline := time.Now().Add(time.Second)
	for {
		var location string
		if err := d.QueryRow(`SELECT COALESCE(ip_location,'') FROM nodes WHERE node_id='n1'`).Scan(&location); err != nil {
			t.Fatal(err)
		}
		if location == "美国-山景城" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stored location = %q", location)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHandleExtendedReport(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	body := `{"schema_version":"1","agent_version":"v0.1.1","node_id":"n1","collected_at":"2026-08-18T00:00:00Z","host":{"hostname":"h","load1":0.1,"load5":0.2,"load15":0.3,"mem_used_pct":12.5,"disk_used_pct":13.5},"services":[{"type":"xray","protocols":{"state":"ok","items":["vless"],"source":"config"},"capabilities":{"traffic":{"support":"supported","source":"native_api"},"online_clients":{"support":"supported","source":"native_api"}},"telemetry":{"traffic":{"state":"ok","tx_bytes":0,"rx_bytes":0},"online_clients":{"state":"ok","value":0}}}]}`
	req := httptest.NewRequest("POST", "/api/v1/agents/report", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleReport(rr, req, AgentIdentity{NodeID: "n1"})
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
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestReportRejectsNodeIDDifferentFromCertificateBoundIdentity(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	manager := configureAgentServer(t, s)
	agent := registerAgent(t, s, manager, authAgentID, "node-bound", time.Now())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/report", strings.NewReader(`{"schema_version":"1","node_id":"node-other","host":{},"services":[]}`))
	attachVerifiedAgent(req, agent)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
