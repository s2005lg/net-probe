package netaddr

import (
	"net/netip"
	"testing"
)

func TestIsPublicAllowsGloballyReachableRegistryExceptions(t *testing.T) {
	for _, raw := range []string{
		"192.0.0.9",
		"192.0.0.10",
		"192.31.196.1",
		"192.52.193.1",
		"192.175.48.1",
		"2001:1::1",
		"2001:1::2",
		"2001:1::3",
		"2001:3::1",
		"2001:4:112::1",
		"2001:20::1",
		"2001:30::1",
		"2620:4f:8000::1",
	} {
		t.Run(raw, func(t *testing.T) {
			if !IsPublic(netip.MustParseAddr(raw)) {
				t.Fatalf("IsPublic(%s) = false", raw)
			}
		})
	}
}

func TestIsPublicStillRejectsRequiredNonRoutableRanges(t *testing.T) {
	for _, raw := range []string{
		"100.64.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"2001:2::1",
		"2001:db8::1",
	} {
		t.Run(raw, func(t *testing.T) {
			if IsPublic(netip.MustParseAddr(raw)) {
				t.Fatalf("IsPublic(%s) = true", raw)
			}
		})
	}
}
