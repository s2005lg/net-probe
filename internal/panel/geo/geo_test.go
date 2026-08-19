package geo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	if got := Format(Location{Country: "美国", RegionName: "加利福尼亚州", City: "洛杉矶"}); got != "美国-洛杉矶" {
		t.Fatalf("format = %q", got)
	}
	if got := Format(Location{Country: "美国"}); got != "美国" {
		t.Fatalf("format country only = %q", got)
	}
}

func TestIsPrivateIP(t *testing.T) {
	if !IsPrivateIP("192.168.1.1") {
		t.Fatal("expected private IPv4")
	}
	if IsPrivateIP("8.8.8.8") {
		t.Fatal("expected public IPv4")
	}
}

func TestLookup(t *testing.T) {
	old := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"status":"success","country":"美国","regionName":"加利福尼亚州","city":"洛杉矶"}`)),
		}, nil
	})}
	defer func() { httpClient = old }()

	loc, err := Lookup(context.Background(), "8.8.8.8")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if Format(loc) != "美国-洛杉矶" {
		t.Fatalf("loc=%+v", loc)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
