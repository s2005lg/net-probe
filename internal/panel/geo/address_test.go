package geo

import (
	"testing"

	"github.com/s2005lg/net-probe/internal/report"
)

func TestEffectiveIPPrecedence(t *testing.T) {
	tests := []struct {
		name string
		host report.Host
		want string
	}{
		{"egress IPv4", report.Host{EgressIPv4: "8.8.8.8", EgressIPv6: "2001:4860:4860::8888", IPv4: "10.0.0.2"}, "8.8.8.8"},
		{"egress IPv6", report.Host{EgressIPv6: "2001:4860:4860::8888", IPv4: "10.0.0.2"}, "2001:4860:4860::8888"},
		{"legacy IPv4", report.Host{IPv4: "1.1.1.1", IPv6: "2001:4860:4860::8888"}, "1.1.1.1"},
		{"legacy IPv6", report.Host{IPv6: "2001:4860:4860::8888"}, "2001:4860:4860::8888"},
		{"malformed egress falls through", report.Host{EgressIPv4: "not-an-ip", EgressIPv6: "2001:4860:4860::8888"}, "2001:4860:4860::8888"},
		{"canonicalizes mapped IPv4", report.Host{EgressIPv4: "::ffff:8.8.8.8"}, "8.8.8.8"},
		{"preserves legacy private IPv4", report.Host{IPv4: "10.0.0.2"}, "10.0.0.2"},
		{"empty", report.Host{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveIP(tt.host); got != tt.want {
				t.Fatalf("EffectiveIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsPublicIPRejectsReservedRanges(t *testing.T) {
	for _, ip := range []string{"", "bad", "127.0.0.1", "10.0.0.1", "169.254.1.1", "224.0.0.1", "::1", "fe80::1", "ff02::1"} {
		if IsPublicIP(ip) {
			t.Fatalf("IsPublicIP(%q) = true", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "2001:4860:4860::8888"} {
		if !IsPublicIP(ip) {
			t.Fatalf("IsPublicIP(%q) = false", ip)
		}
	}
}
