package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/sink"
)

func TestPanelIntegration(t *testing.T) {
	t.Setenv("NP_TOKEN", "secret")

	var gotAuth, gotAgent, gotPath string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAgent = r.Header.Get("X-Agent-Id")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Agent.NodeID = "node-1"
	cfg.Sinks = []config.Sink{{Type: "panel", URL: srv.URL, TokenEnv: "NP_TOKEN"}}

	rep, err := Build(context.Background(), cfg, "0.1.0", fakeRunner{})
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
	if gotAuth != "Bearer secret" {
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
}
