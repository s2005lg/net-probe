package api

import (
	"crypto/tls"
	"net/http"
)

// NewTLSServer builds the shared HTTPS listener used by browser, enrollment,
// reporting, and control routes. Handler-level Agent authentication is kept
// separate from the optional certificate verification performed by TLS.
func NewTLSServer(addr string, handler http.Handler, tlsConfig *tls.Config) *http.Server {
	return &http.Server{
		Addr:      addr,
		Handler:   handler,
		TLSConfig: tlsConfig,
	}
}
