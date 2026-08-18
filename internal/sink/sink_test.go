package sink

import (
	"context"
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
	t.Setenv("T", "secret")
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotAuth = req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})
	s, err := New(config.Sink{Type: "panel", URL: "http://panel.example.com", TokenEnv: "T"})
	if err != nil {
		t.Fatal(err)
	}
	hs, ok := s.(*httpSink)
	if !ok {
		t.Fatalf("New returned %T, want *httpSink", s)
	}
	hs.client = &http.Client{Transport: rt}
	if err := s.Send(context.Background(), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("auth = %q", gotAuth)
	}
}
