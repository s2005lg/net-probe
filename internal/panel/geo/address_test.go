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
		{"IPv6 in IPv4 field falls through", report.Host{EgressIPv4: "2001:4860:4860::8888", EgressIPv6: "2606:4700:4700::1111"}, "2606:4700:4700::1111"},
		{"IPv4 in IPv6 field falls through", report.Host{EgressIPv6: "8.8.8.8", IPv4: "1.1.1.1"}, "1.1.1.1"},
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
	for _, ip := range []string{
		"", "bad", "0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.1.1",
		"192.0.0.1", "192.0.2.1", "192.168.1.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "64:ff9b:1::1", "100::1", "2001::1", "2001:2::1", "2001:10::1",
		"2001:db8::1", "3fff::1", "5f00::1", "fc00::1", "fe80::1", "ff02::1",
	} {
		if IsPublicIP(ip) {
			t.Fatalf("IsPublicIP(%q) = true", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "::ffff:8.8.8.8", "2001:4860:4860::8888"} {
		if !IsPublicIP(ip) {
			t.Fatalf("IsPublicIP(%q) = false", ip)
		}
	}
}
