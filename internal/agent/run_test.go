package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/egressip"
)

type fakeRunner struct{}

func (fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	return "", nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRunExitZero(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})

	cfg := config.Default()
	cfg.Collect.EgressIP.Enabled = false
	cfg.Sinks = []config.Sink{{Type: "webhook", URL: "http://127.0.0.1/unused"}}
	rc := Run(context.Background(), cfg, "0.1.0", fakeRunner{})
	if rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
}

func TestPersistedNodeID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	first := persistedNodeID()
	if first == "" {
		t.Fatal("empty persisted node id")
	}
	if !strings.HasPrefix(first, "g-") {
		t.Fatalf("id = %q", first)
	}
	second := persistedNodeID()
	if first != second {
		t.Fatalf("first=%q second=%q", first, second)
	}
	if _, err := os.Stat(filepath.Join(ConfigDir(), "node-id")); err != nil {
		t.Fatalf("node-id file missing: %v", err)
	}
}

func TestBuildLogsTelemetryDiagnosticsLocallyWithoutLeakingThem(t *testing.T) {
	cfg := config.Default()
	cfg.Collect.EgressIP.Enabled = false
	cfg.Detect.CustomDir = t.TempDir()
	cfg.Stats.Services = map[string]config.StatsService{
		"xray": {Endpoint: "127.0.0.1:10085"},
	}
	runner := diagnosticRunner{responses: map[string]runnerResult{
		"systemctl list-unit-files --type=service --no-legend --no-pager":                               {out: "xray.service enabled\n"},
		"systemctl show xray --property=ActiveState,SubState,UnitFileState,NRestarts,MainPID,ExecStart": {out: "ActiveState=active\nUnitFileState=enabled\nMainPID=10"},
		"xray api statsquery -s 127.0.0.1:10085 -pattern >>>traffic>>>":                                 {err: errors.New("controlled statistics failure")},
		"xray api statsonlineiplist -s 127.0.0.1:10085 -all":                                            {err: errors.New("controlled statistics failure")},
	}}
	var logs []string
	rep, err := build(context.Background(), cfg, "0.1.0", runner, func(format string, args ...any) {
		logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 || !strings.Contains(strings.Join(logs, "\n"), "controlled statistics failure") {
		t.Fatalf("logs = %q", logs)
	}
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "controlled statistics failure") {
		t.Fatalf("report leaked diagnostic: %s", body)
	}
	if !strings.Contains(string(body), `"error_code":"command_failed"`) {
		t.Fatalf("report missing safe error code: %s", body)
	}
}

func TestBuildReportsResolvedEgressIPs(t *testing.T) {
	cfg := config.Default()
	cfg.Detect.CustomDir = t.TempDir()
	cfg.Sinks = []config.Sink{{Type: "webhook", URL: "https://example.com/report"}}
	rep, err := buildWithEgress(context.Background(), cfg, "test", fakeRunner{}, nil,
		func(context.Context, egressip.Options) egressip.Result {
			return egressip.Result{IPv4: "8.8.8.8", IPv6: "2001:4860:4860::8888"}
		})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Host.EgressIPv4 != "8.8.8.8" || rep.Host.EgressIPv6 != "2001:4860:4860::8888" {
		t.Fatalf("host = %+v", rep.Host)
	}
}

func TestBuildPassesConfiguredEgressOptions(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	cfg := config.Default()
	cfg.Detect.CustomDir = t.TempDir()
	cfg.Collect.EgressIP.RefreshInterval = "7h"
	cfg.Collect.EgressIP.Timeout = "4s"
	cfg.Collect.EgressIP.IPv4Endpoints = []string{"https://v4-a.test", "https://v4-b.test"}
	cfg.Collect.EgressIP.IPv6Endpoints = []string{"https://v6.test"}
	var got egressip.Options
	_, err := buildWithEgress(context.Background(), cfg, "test", fakeRunner{}, func(string, ...any) {},
		func(_ context.Context, opts egressip.Options) egressip.Result {
			got = opts
			return egressip.Result{}
		})
	if err != nil {
		t.Fatal(err)
	}
	wantCache := filepath.Join(configHome, "net-probe", "egress-ip-cache.json")
	if !got.Enabled || got.RefreshInterval != 7*time.Hour || got.Timeout != 4*time.Second || got.CachePath != wantCache {
		t.Fatalf("options = %+v", got)
	}
	if strings.Join(got.IPv4Endpoints, ",") != "https://v4-a.test,https://v4-b.test" || strings.Join(got.IPv6Endpoints, ",") != "https://v6.test" {
		t.Fatalf("endpoints = %v, %v", got.IPv4Endpoints, got.IPv6Endpoints)
	}
	if got.Logf == nil {
		t.Fatal("Logf = nil")
	}
}

type runnerResult struct {
	out string
	err error
}

type diagnosticRunner struct{ responses map[string]runnerResult }

func (r diagnosticRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	result := r.responses[name+" "+strings.Join(args, " ")]
	return result.out, result.err
}
