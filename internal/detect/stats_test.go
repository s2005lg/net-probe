package detect

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s2005lg/net-probe/internal/report"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withRoundTripper(t *testing.T, fn roundTripFunc) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = fn
	t.Cleanup(func() { http.DefaultTransport = old })
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestHysteria2TelemetryKeepsTrafficWhenOnlineFails(t *testing.T) {
	withRoundTripper(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/traffic" {
			return jsonResponse(http.StatusOK, `{"u":{"tx":0,"rx":0}}`), nil
		}
		return jsonResponse(http.StatusUnauthorized, `denied`), nil
	})
	result := CollectTelemetryWithRunner(context.Background(), "hysteria2", "http://unused", "", fakeRunner{})
	got := result.Telemetry
	if got.Traffic.State != report.ObservationOK || *got.Traffic.TxBytes != 0 || *got.Traffic.RxBytes != 0 {
		t.Fatalf("traffic = %+v", got.Traffic)
	}
	if got.OnlineClients.State != report.ObservationError || got.OnlineClients.ErrorCode != "unauthorized" {
		t.Fatalf("online = %+v", got.OnlineClients)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Metric != "online_clients" || result.Diagnostics[0].Err == nil {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestHysteria2TelemetryKeepsOnlineWhenTrafficFails(t *testing.T) {
	withRoundTripper(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/traffic" {
			return nil, errors.New("connection reset")
		}
		return jsonResponse(http.StatusOK, `{"u":0}`), nil
	})
	result := CollectTelemetryWithRunner(context.Background(), "hysteria2", "http://unused", "", fakeRunner{})
	got := result.Telemetry
	if got.Traffic.State != report.ObservationError || got.Traffic.ErrorCode != "connection_failed" {
		t.Fatalf("traffic = %+v", got.Traffic)
	}
	if got.OnlineClients.State != report.ObservationOK || got.OnlineClients.Value == nil || *got.OnlineClients.Value != 0 {
		t.Fatalf("online = %+v", got.OnlineClients)
	}
}

func TestSingBoxTelemetryHasNoOnlineObservation(t *testing.T) {
	withRoundTripper(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"up":7,"down":9}`), nil
	})
	result := CollectTelemetryWithRunner(context.Background(), "sing-box", "http://unused", "", fakeRunner{})
	got := result.Telemetry
	if got.Traffic.State != report.ObservationOK || got.OnlineClients != nil {
		t.Fatalf("telemetry = %+v", got)
	}
}

func TestXrayTelemetryKeepsOnlineWhenTrafficFails(t *testing.T) {
	r := scriptedRunner{responses: map[string]runnerResponse{
		"xray api stats query -s 127.0.0.1:8080":            {err: errors.New("stats unavailable")},
		"xray api statsonlineiplist -s 127.0.0.1:8080 -all": {out: `{"users":[{"ips":[{"ip":"1.2.3.4"}]}]}`},
	}}
	result := CollectTelemetryWithRunner(context.Background(), "xray", "127.0.0.1:8080", "", r)
	got := result.Telemetry
	if got.Traffic.State != report.ObservationError || got.Traffic.ErrorCode != "command_failed" {
		t.Fatalf("traffic = %+v", got.Traffic)
	}
	if got.OnlineClients.State != report.ObservationOK || got.OnlineClients.Value == nil || *got.OnlineClients.Value != 1 {
		t.Fatalf("online = %+v", got.OnlineClients)
	}
}

type runnerResponse struct {
	out string
	err error
}

type scriptedRunner struct{ responses map[string]runnerResponse }

func (r scriptedRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	response := r.responses[name+" "+strings.Join(args, " ")]
	return response.out, response.err
}

func TestHysteria2StatsNoScheme(t *testing.T) {
	withRoundTripper(t, func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.String(), "http://127.0.0.1:8080/") {
			t.Fatalf("url = %q", req.URL.String())
		}
		if req.URL.Path == "/traffic" {
			return jsonResponse(http.StatusOK, `{"u":{"tx":1,"rx":2}}`), nil
		}
		return jsonResponse(http.StatusOK, `{}`), nil
	})
	if _, err := CollectStats(context.Background(), "hysteria2", "127.0.0.1:8080", ""); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverStats(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "hysteria.yaml")
	_ = os.WriteFile(cfg, []byte("trafficStats:\n  listen: 127.0.0.1:9999\n  secret: abc\n"), 0o600)
	tmpl := Template{StatsKind: "hysteria2", StatsConfigPaths: []string{cfg}}
	ep, ok := discoverStats(tmpl)
	if !ok || ep.Endpoint != "127.0.0.1:9999" || ep.Secret != "abc" {
		t.Fatalf("ep=%+v ok=%v", ep, ok)
	}
}
