package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/s2005lg/net-probe/internal/config"
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
	cfg.Sinks = []config.Sink{{Type: "webhook", URL: "http://127.0.0.1/unused"}}
	rc := Run(context.Background(), cfg, "0.1.0", fakeRunner{})
	if rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
}
