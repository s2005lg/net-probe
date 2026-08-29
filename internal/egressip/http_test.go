package egressip

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDiscoverValidatesFamily(t *testing.T) {
	tests := []struct {
		name   string
		family Family
		body   string
		want   string
		ok     bool
	}{
		{"ipv4", IPv4, " 8.8.8.8\n", "8.8.8.8", true},
		{"ipv6", IPv6, "2001:4860:4860::8888\n", "2001:4860:4860::8888", true},
		{"wrong family", IPv4, "2001:4860:4860::8888", "", false},
		{"private", IPv4, "192.168.1.2", "", false},
		{"cgnat", IPv4, "100.64.0.1", "", false},
		{"ietf protocol assignment", IPv4, "192.0.0.1", "", false},
		{"documentation 1", IPv4, "192.0.2.1", "", false},
		{"benchmarking", IPv4, "198.18.0.1", "", false},
		{"documentation 2", IPv4, "198.51.100.1", "", false},
		{"documentation 3", IPv4, "203.0.113.1", "", false},
		{"reserved", IPv4, "240.0.0.1", "", false},
		{"loopback", IPv6, "::1", "", false},
		{"IPv6 discard only", IPv6, "100::1", "", false},
		{"IPv6 benchmarking", IPv6, "2001:2::1", "", false},
		{"IPv6 documentation", IPv6, "2001:db8::1", "", false},
		{"IPv6 documentation 2", IPv6, "3fff::1", "", false},
		{"IPv6 segment routing", IPv6, "5f00::1", "", false},
		{"mapped IPv4", IPv4, "::ffff:8.8.8.8", "8.8.8.8", true},
		{"malformed", IPv4, "not-an-ip", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, tt.body), nil
			})}
			got, err := (Fetcher{Client: client}).Discover(context.Background(), tt.family, []string{"https://provider.test/ip"})
			if (err == nil) != tt.ok || got != tt.want {
				t.Fatalf("Discover() = %q, %v", got, err)
			}
		})
	}
}

func TestDiscoverFallsBackInOrder(t *testing.T) {
	var hosts []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Host)
		if req.URL.Host == "first.test" {
			return response(http.StatusServiceUnavailable, "unavailable"), nil
		}
		return response(http.StatusOK, "8.8.4.4"), nil
	})}
	got, err := (Fetcher{Client: client}).Discover(context.Background(), IPv4, []string{
		"https://first.test/ip", "https://second.test/ip",
	})
	if err != nil || got != "8.8.4.4" || !reflect.DeepEqual(hosts, []string{"first.test", "second.test"}) {
		t.Fatalf("got=%q err=%v hosts=%v", got, err, hosts)
	}
}

func TestDiscoverRejectsOversizedBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, strings.Repeat("8", 65)), nil
	})}

	got, err := (Fetcher{Client: client}).Discover(context.Background(), IPv4, []string{"https://provider.test/ip"})
	if err == nil || got != "" {
		t.Fatalf("Discover() = %q, %v", got, err)
	}
}

func TestDiscoverDoesNotExposeMalformedResponseBody(t *testing.T) {
	const body = "distinct-malformed-provider-payload"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, body), nil
	})}

	_, err := (Fetcher{Client: client}).Discover(context.Background(), IPv4, []string{"https://provider.test/ip"})
	if err == nil {
		t.Fatal("Discover() error = nil, want malformed response rejection")
	}
	if strings.Contains(err.Error(), body) {
		t.Fatalf("Discover() error exposed response body: %v", err)
	}
}

func TestHTTPClientRejectsHTTPSDowngrade(t *testing.T) {
	client := NewHTTPClient(time.Second)
	var schemes []string
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		schemes = append(schemes, req.URL.Scheme)
		if req.URL.Scheme == "https" {
			resp := response(http.StatusFound, "")
			resp.Header.Set("Location", "http://provider.test/ip")
			return resp, nil
		}
		return response(http.StatusOK, "8.8.8.8"), nil
	})

	resp, err := client.Get("https://provider.test/ip")
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || !reflect.DeepEqual(schemes, []string{"https"}) {
		t.Fatalf("Get() error = %v, schemes = %v; want HTTPS-to-HTTP redirect rejection", err, schemes)
	}
}

func TestHTTPClientRejectsLoopbackHTTPRedirectToExternalHTTP(t *testing.T) {
	client := NewHTTPClient(time.Second)
	var hosts []string
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Hostname())
		resp := response(http.StatusFound, "")
		resp.Header.Set("Location", "http://public.example/ip")
		return resp, nil
	})

	resp, err := client.Get("http://localhost/ip")
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || !reflect.DeepEqual(hosts, []string{"localhost"}) {
		t.Fatalf("Get() error = %v, hosts = %v; want external cleartext redirect rejection", err, hosts)
	}
}

func TestHTTPClientAllowsLoopbackHTTPRedirect(t *testing.T) {
	client := NewHTTPClient(time.Second)
	var hosts []string
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Hostname())
		if len(hosts) == 1 {
			resp := response(http.StatusFound, "")
			resp.Header.Set("Location", "http://127.0.0.1/ip")
			return resp, nil
		}
		return response(http.StatusOK, "8.8.8.8"), nil
	})

	resp, err := client.Get("http://localhost/ip")
	if resp != nil {
		resp.Body.Close()
	}
	if err != nil || !reflect.DeepEqual(hosts, []string{"localhost", "127.0.0.1"}) {
		t.Fatalf("Get() error = %v, hosts = %v; want loopback cleartext redirect", err, hosts)
	}
}

func TestHTTPClientAllowsThreeRedirects(t *testing.T) {
	client := NewHTTPClient(time.Second)
	var requests int
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests <= 3 {
			resp := response(http.StatusFound, "")
			resp.Header.Set("Location", "https://provider.test/ip")
			return resp, nil
		}
		return response(http.StatusOK, "8.8.8.8"), nil
	})

	resp, err := client.Get("https://provider.test/ip")
	if resp != nil {
		resp.Body.Close()
	}
	if err != nil || requests != 4 {
		t.Fatalf("Get() error = %v, requests = %d; want three redirects followed", err, requests)
	}
}

func TestHTTPClientRejectsFourthRedirect(t *testing.T) {
	client := NewHTTPClient(time.Second)
	var requests int
	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		resp := response(http.StatusFound, "")
		resp.Header.Set("Location", "https://provider.test/ip")
		return resp, nil
	})

	resp, err := client.Get("https://provider.test/ip")
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || requests != 4 {
		t.Fatalf("Get() error = %v, requests = %d; want fourth redirect rejected", err, requests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
