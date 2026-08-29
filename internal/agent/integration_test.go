package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/sink"
)

func TestPanelIntegration(t *testing.T) {
	const fixtureUUID = "fixture-vless-uuid"
	const statsSecret = "fixture-stats-secret"
	const panelToken = "fixture-panel-token"
	const configBody = `{"inbounds":[{"protocol":"vless","settings":{"clients":[{"id":"fixture-vless-uuid"}]}}]}`
	t.Setenv("NP_TOKEN", panelToken)

	var gotAuth, gotAgent, gotPath, rawBody string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAgent = r.Header.Get("X-Agent-Id")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		rawBody = string(b)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	configPath := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Collect.EgressIP.Enabled = false
	cfg.Detect.CustomDir = t.TempDir()
	cfg.Agent.NodeID = "node-1"
	cfg.Sinks = []config.Sink{{Type: "panel", URL: srv.URL, TokenEnv: "NP_TOKEN"}}
	cfg.Stats.Services = map[string]config.StatsService{
		"xray": {Endpoint: "127.0.0.1:10085", Secret: statsSecret},
	}

	runner := diagnosticRunner{responses: map[string]runnerResult{
		"systemctl list-unit-files --type=service --no-legend --no-pager": {out: "xray.service enabled\n"},
		"systemctl show xray --property=ActiveState,SubState,UnitFileState,NRestarts,MainPID,ExecStart": {
			out: "ActiveState=active\nUnitFileState=enabled\nMainPID=10\nExecStart={ path=/usr/bin/xray ; argv[]=/usr/bin/xray run -config " + configPath + " ; ignore_errors=no }",
		},
		"xray api statsquery -s 127.0.0.1:10085 -pattern >>>traffic>>>": {out: `{"stat":[{"name":"inbound>>>edge>>>traffic>>>uplink","value":"7"},{"name":"inbound>>>edge>>>traffic>>>downlink","value":"9"}]}`},
		"xray api statsonlineiplist -s 127.0.0.1:10085 -all":            {out: `{"users":[{"ips":[{"ip":"192.0.2.1"}]}]}`},
	}}
	rep, err := Build(context.Background(), cfg, "0.1.0", runner)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}

	s, err := sink.New(cfg.Sinks[0], rep.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), payload); err != nil {
		t.Fatal(err)
	}

	if gotPath != "/api/v1/agents/report" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer "+panelToken {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotAgent != "node-1" {
		t.Fatalf("agent id = %q", gotAgent)
	}
	if body["schema_version"] != "1" || body["agent_version"] != "0.1.0" || body["node_id"] != "node-1" {
		t.Fatalf("top-level fields = %v", body)
	}
	host, _ := body["host"].(map[string]any)
	if host == nil || host["hostname"] == nil {
		t.Fatalf("missing host.hostname: %v", body)
	}
	if _, ok := body["services"].([]any); !ok {
		t.Fatalf("services missing or wrong type: %v", body)
	}
	services := body["services"].([]any)
	if len(services) != 1 {
		t.Fatalf("services=%v", services)
	}
	xray, _ := services[0].(map[string]any)
	if xray["type"] != "xray" {
		t.Fatalf("service=%v", xray)
	}
	protocols, _ := xray["protocols"].(map[string]any)
	items, _ := protocols["items"].([]any)
	if len(items) != 1 || items[0] != "vless" {
		t.Fatalf("protocols=%v", protocols)
	}
	capabilities, _ := xray["capabilities"].(map[string]any)
	if capabilities["traffic"] == nil || capabilities["online_clients"] == nil {
		t.Fatalf("capabilities=%v", capabilities)
	}
	telemetry, _ := xray["telemetry"].(map[string]any)
	if telemetry["traffic"] == nil || telemetry["online_clients"] == nil {
		t.Fatalf("telemetry=%v", telemetry)
	}
	stats, _ := xray["stats"].(map[string]any)
	if stats["tx"] != float64(7) || stats["rx"] != float64(9) || stats["online_clients"] != float64(1) {
		t.Fatalf("stats=%v", stats)
	}
	for _, private := range []string{fixtureUUID, statsSecret, gotAuth, configBody} {
		if strings.Contains(rawBody, private) {
			t.Fatalf("report leaked private fixture value %q: %s", private, rawBody)
		}
	}
}
