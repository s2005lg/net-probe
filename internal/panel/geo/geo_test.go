package geo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestIPWhoisProviderLookup(t *testing.T) {
	var gotURL, gotAuth string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotAuth = req.Header.Get("Authorization")
		return response(http.StatusOK, `{"success":true,"country":"美国","region":"加利福尼亚州","city":"洛杉矶"}`), nil
	})}
	p := NewIPWhoisProvider(client, "https://geo.test/{ip}?lang=zh-CN", "secret")
	loc, err := p.Lookup(context.Background(), "2001:4860:4860::8888")
	if err != nil || Format(loc) != "美国-洛杉矶" {
		t.Fatalf("location=%+v err=%v", loc, err)
	}
	if gotURL != "https://geo.test/2001:4860:4860::8888?lang=zh-CN" || gotAuth != "Bearer secret" {
		t.Fatalf("url=%q auth=%q", gotURL, gotAuth)
	}
}

func TestIPWhoisProviderRejectsApplicationError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"success":false,"message":"rate limit"}`), nil
	})}
	_, err := NewIPWhoisProvider(client, "https://geo.test/{ip}", "").Lookup(context.Background(), "8.8.8.8")
	if err == nil {
		t.Fatal("expected provider error")
	}
}

func TestIPWhoisProviderRejectsInvalidResponsesWithoutLeakingBody(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"non-2xx", http.StatusTooManyRequests, `provider-secret-body`},
		{"malformed JSON", http.StatusOK, `{`},
		{"oversized body", http.StatusOK, `{"success":true,"country":"` + strings.Repeat("x", 1<<20) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(tt.status, tt.body), nil
			})}
			_, err := NewIPWhoisProvider(client, "https://geo.test/{ip}", "").Lookup(context.Background(), "8.8.8.8")
			if err == nil {
				t.Fatal("expected provider error")
			}
			if strings.Contains(err.Error(), "provider-secret-body") {
				t.Fatalf("error leaked provider body: %v", err)
			}
		})
	}
}

func TestIPWhoisProviderRejectsInvalidIP(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request should not be sent")
		return nil, nil
	})}
	_, err := NewIPWhoisProvider(client, "https://geo.test/{ip}", "").Lookup(context.Background(), "not-an-ip")
	if err == nil {
		t.Fatal("expected invalid IP error")
	}
}

func TestNewHTTPClientProtectsHTTPSRedirectDowngrade(t *testing.T) {
	client := NewHTTPClient(4 * time.Second)
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Scheme {
		case "https":
			resp := response(http.StatusFound, "")
			resp.Header.Set("Location", "http://geo.test/next")
			return resp, nil
		default:
			t.Fatalf("unexpected redirected request: %s", req.URL)
			return nil, nil
		}
	})
	_, err := client.Get("https://geo.test/start")
	if err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("Get() error = %v", err)
	}
}

func TestNewHTTPClientRejectsUnsafeRedirectTarget(t *testing.T) {
	client := NewHTTPClient(4 * time.Second)
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() != "127.0.0.1" {
			t.Fatalf("unexpected redirected request: %s", req.URL)
		}
		resp := response(http.StatusFound, "")
		resp.Header.Set("Location", "http://geo.test/next")
		return resp, nil
	})
	_, err := client.Get("http://127.0.0.1/start")
	if err == nil || !strings.Contains(err.Error(), "redirect target") {
		t.Fatalf("Get() error = %v", err)
	}
}

func TestNewHTTPClientAllowsThreeRedirectsBeforeStopping(t *testing.T) {
	client := NewHTTPClient(4 * time.Second)
	requests := 0
	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		resp := response(http.StatusFound, "")
		resp.Header.Set("Location", "https://geo.test/next")
		return resp, nil
	})
	_, err := client.Get("https://geo.test/start")
	if err == nil || !strings.Contains(err.Error(), "3 redirects") {
		t.Fatalf("Get() error = %v", err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4", requests)
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

var _ Provider = (*IPWhoisProvider)(nil)
