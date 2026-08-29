package urlpolicy

import (
	"net/url"
	"strings"
)

// IsSecureEndpoint permits HTTPS everywhere and cleartext HTTP only on the
// exact loopback hostnames accepted by Agent configuration.
func IsSecureEndpoint(u *url.URL) bool {
	if u == nil || u.Hostname() == "" {
		return false
	}
	if strings.EqualFold(u.Scheme, "https") {
		return true
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
