package detect

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHysteria2Stats(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := ""
		switch req.URL.Path {
		case "/traffic":
			body = `{"u1":{"tx":100,"rx":50},"u2":{"tx":300,"rx":150}}`
		case "/online":
			body = `{"u1":2,"u2":1}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	s, err := CollectStats(context.Background(), "hysteria2", "http://unused", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Tx != 400 || s.Rx != 200 || s.OnlineClients != 3 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestSingBoxStats(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"up":700,"down":300}`)),
		}, nil
	})
	s, err := CollectStats(context.Background(), "sing-box", "http://unused", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Tx != 700 || s.Rx != 300 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestXrayStats(t *testing.T) {
	r := fakeRunner{out: map[string]string{
		"xray api stats query -s 127.0.0.1:8080": "user>>>u1>>>traffic>>>uplink 700\nuser>>>u1>>>traffic>>>downlink 300\n",
	}}
	s, err := xrayStats(context.Background(), r, "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	if s.Tx != 700 || s.Rx != 300 {
		t.Fatalf("stats = %+v", s)
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
