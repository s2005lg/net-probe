package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type Location struct {
	Country    string
	RegionName string
	City       string
}

const maxResponseBody = 1 << 20

const defaultIPWhoisURL = "https://ipwho.is/{ip}?lang=zh-CN"

type Provider interface {
	Lookup(context.Context, string) (Location, error)
}

type IPWhoisProvider struct {
	client      *http.Client
	urlTemplate string
	bearerToken string
}

type ipWhoisResponse struct {
	Success bool   `json:"success"`
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
}

func NewIPWhoisProvider(client *http.Client, urlTemplate, bearerToken string) *IPWhoisProvider {
	if client == nil {
		client = NewHTTPClient(4 * time.Second)
	}
	return &IPWhoisProvider{client: client, urlTemplate: urlTemplate, bearerToken: bearerToken}
}

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return fmt.Errorf("geo provider stopped after 3 redirects")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
				return fmt.Errorf("geo provider redirect downgrade rejected")
			}
			if !safeRedirectTarget(req.URL) {
				return fmt.Errorf("geo provider redirect target rejected")
			}
			return nil
		},
	}
}

// Lookup preserves source compatibility until Panel startup injects a configured Provider.
func Lookup(ctx context.Context, ip string) (Location, error) {
	return NewIPWhoisProvider(NewHTTPClient(4*time.Second), defaultIPWhoisURL, "").Lookup(ctx, ip)
}

func safeRedirectTarget(u *url.URL) bool {
	if u.Hostname() == "" {
		return false
	}
	if strings.EqualFold(u.Scheme, "https") {
		return true
	}
	return strings.EqualFold(u.Scheme, "http") && isLocalHost(u.Hostname())
}

func isLocalHost(hostname string) bool {
	switch strings.ToLower(hostname) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func (p *IPWhoisProvider) Lookup(ctx context.Context, ip string) (Location, error) {
	parsed, err := netip.ParseAddr(ip)
	if err != nil {
		return Location{}, fmt.Errorf("invalid IP address")
	}
	endpoint := strings.Replace(p.urlTemplate, "{ip}", parsed.Unmap().String(), 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Location{}, fmt.Errorf("invalid geo provider URL")
	}
	if p.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.bearerToken)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return Location{}, fmt.Errorf("geo provider request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Location{}, fmt.Errorf("geo provider returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil || len(body) > maxResponseBody {
		return Location{}, fmt.Errorf("invalid geo provider response")
	}
	var out ipWhoisResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return Location{}, fmt.Errorf("invalid geo provider response")
	}
	if !out.Success {
		return Location{}, fmt.Errorf("geo lookup failed")
	}
	return Location{Country: out.Country, RegionName: out.Region, City: out.City}, nil
}

func Format(loc Location) string {
	parts := make([]string, 0, 2)
	if loc.Country != "" {
		parts = append(parts, loc.Country)
	}
	if loc.City != "" {
		parts = append(parts, loc.City)
	} else if loc.RegionName != "" {
		parts = append(parts, loc.RegionName)
	}
	return strings.Join(parts, "-")
}

func IsPrivateIP(ip string) bool {
	parsed, err := netip.ParseAddr(ip)
	return err == nil && parsed.IsPrivate()
}
