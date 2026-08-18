package version

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFetchHysteria2(t *testing.T) {
	oldClient := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/repos/apernet/hysteria/releases/latest" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"tag_name":"app/v2.12.1"}`)),
		}, nil
	})}
	defer func() { httpClient = oldClient }()

	got, err := FetchLatest("hysteria2")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got != "2.12.1" {
		t.Fatalf("got %q", got)
	}
}

func TestUnsupportedService(t *testing.T) {
	if _, err := FetchLatest("missing"); err == nil {
		t.Fatal("expected error")
	}
}
