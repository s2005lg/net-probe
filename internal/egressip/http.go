package egressip

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const maxResponseBytes = 64

type Family uint8

const (
	IPv4 Family = 4
	IPv6 Family = 6
)

type Fetcher struct {
	Client *http.Client
}

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return fmt.Errorf("egress IP discovery stopped after three redirects")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
				return fmt.Errorf("egress IP discovery rejects HTTPS-to-HTTP redirects")
			}
			return nil
		},
	}
}

func (f Fetcher) Discover(ctx context.Context, family Family, endpoints []string) (string, error) {
	if f.Client == nil {
		return "", fmt.Errorf("egress IP discovery requires an HTTP client")
	}

	var lastErr error
	for _, endpoint := range endpoints {
		addr, err := f.discoverEndpoint(ctx, family, endpoint)
		if err == nil {
			return addr, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return "", fmt.Errorf("egress IP discovery has no endpoints")
	}
	return "", fmt.Errorf("egress IP discovery failed: %w", lastErr)
}

func (f Fetcher) discoverEndpoint(ctx context.Context, family Family, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request %q: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("request %q returned HTTP %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read response from %q: %w", endpoint, err)
	}
	if len(body) > maxResponseBytes {
		return "", fmt.Errorf("response from %q exceeds %d bytes", endpoint, maxResponseBytes)
	}

	addr, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil {
		return "", fmt.Errorf("response from %q is not an IP address", endpoint)
	}
	addr = addr.Unmap()
	if !matchesFamily(addr, family) {
		return "", fmt.Errorf("response from %q has the wrong address family", endpoint)
	}
	if !isPublic(addr) {
		return "", fmt.Errorf("response from %q is not a public address", endpoint)
	}
	return addr.String(), nil
}

func matchesFamily(addr netip.Addr, family Family) bool {
	switch family {
	case IPv4:
		return addr.Is4()
	case IPv6:
		return addr.Is6()
	default:
		return false
	}
}

func isPublic(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() &&
		!addr.IsPrivate() &&
		!addr.IsLoopback() &&
		!addr.IsLinkLocalUnicast() &&
		!addr.IsMulticast() &&
		!addr.IsUnspecified()
}
