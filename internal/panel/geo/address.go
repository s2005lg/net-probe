package geo

import (
	"net/netip"

	"github.com/s2005lg/net-probe/internal/report"
)

// EffectiveIP returns the first parseable reported address in the Panel's
// egress-first precedence order.
func EffectiveIP(host report.Host) string {
	for _, candidate := range []string{host.EgressIPv4, host.EgressIPv6, host.IPv4, host.IPv6} {
		if addr, ok := parseIP(candidate); ok {
			return addr.String()
		}
	}
	return ""
}

// IsPublicIP reports whether ip is eligible for external geolocation.
func IsPublicIP(ip string) bool {
	addr, ok := parseIP(ip)
	return ok && addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !addr.IsMulticast() && !addr.IsUnspecified()
}

func parseIP(ip string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
