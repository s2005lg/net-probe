package sink

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/s2005lg/net-probe/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPanelSink(t *testing.T) {
	var gotAuth string
	var gotAgent string
	var gotPath string
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotAuth = req.Header.Get("Authorization")
		gotAgent = req.Header.Get("X-Agent-Id")
		gotPath = req.URL.Path
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	s, err := NewPanel("https://panel.example.com", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	hs, ok := s.(*httpSink)
	if !ok {
		t.Fatalf("New returned %T, want *httpSink", s)
	}
	transport, ok := hs.client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatal("Panel sink did not reuse the loaded mTLS configuration")
	}
	hs.client = &http.Client{Transport: rt}
	if err := s.Send(context.Background(), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Fatalf("unexpected bearer auth = %q", gotAuth)
	}
	if gotAgent != "" {
		t.Fatalf("unexpected trusted Agent header = %q", gotAgent)
	}
	if gotPath != "/api/v1/agents/report" {
		t.Fatalf("panel path = %q", gotPath)
	}
}

func TestWebhookSinkKeepsURL(t *testing.T) {
	var gotPath string
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})
	s, err := New(config.Sink{Type: "webhook", URL: "https://example.com/api/push/abc"}, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	hs := s.(*httpSink)
	hs.client = &http.Client{Transport: rt}
	if err := s.Send(context.Background(), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/push/abc" {
		t.Fatalf("webhook path = %q", gotPath)
	}
}

func TestSinkTLSConfig(t *testing.T) {
	t.Setenv("T", "s")
	s, err := New(config.Sink{Type: "webhook", URL: "https://example.com", TokenEnv: "T", TLSSkipVerify: true}, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	hs := s.(*httpSink)
	tr, ok := hs.client.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLSSkipVerify not applied")
	}
}
