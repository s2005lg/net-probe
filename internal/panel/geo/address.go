package geo

import (
	"net/netip"

	"github.com/s2005lg/net-probe/internal/netaddr"
	"github.com/s2005lg/net-probe/internal/report"
)

// EffectiveIP returns the first parseable reported address in the Panel's
// egress-first precedence order.
func EffectiveIP(host report.Host) string {
	for _, candidate := range []struct {
		ip       string
		wantIPv4 bool
	}{
		{ip: host.EgressIPv4, wantIPv4: true},
		{ip: host.EgressIPv6},
		{ip: host.IPv4, wantIPv4: true},
		{ip: host.IPv6},
	} {
		if addr, ok := parseIP(candidate.ip); ok && addr.Is4() == candidate.wantIPv4 {
			return addr.String()
		}
	}
	return ""
}

// IsPublicIP reports whether ip is eligible for external geolocation.
func IsPublicIP(ip string) bool {
	addr, ok := parseIP(ip)
	return ok && netaddr.IsPublic(addr)
}

func parseIP(ip string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
