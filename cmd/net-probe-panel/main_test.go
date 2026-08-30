package main

import (
	"crypto/tls"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/s2005lg/net-probe/internal/panel/config"
)

func TestNewPanelTLSServerUsesPrivatePKIAndOptionalClientVerification(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.PublicURL = "https://panel.example.com:24443"
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	srv, manager, err := newPanelTLSServer(cfg, handler)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Addr != cfg.ListenAddr || srv.Handler == nil {
		t.Fatalf("server = %+v", srv)
	}
	if srv.TLSConfig == nil || srv.TLSConfig.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Fatalf("TLS config = %+v", srv.TLSConfig)
	}
	if manager.ServerCertFile != filepath.Join(cfg.DataDir, "pki", "server.crt") || manager.ServerKeyFile != filepath.Join(cfg.DataDir, "pki", "server.key") {
		t.Fatalf("server identity paths = %q, %q", manager.ServerCertFile, manager.ServerKeyFile)
	}
}

func TestNewGeoRefresherRejectsEmptyTokenEnvironment(t *testing.T) {
	cfg := config.Default()
	cfg.Geo.TokenEnv = "NET_PROBE_GEO_TOKEN_TEST"
	t.Setenv(cfg.Geo.TokenEnv, "")

	_, err := newGeoRefresher(nil, cfg)
	const want = `geo token env "NET_PROBE_GEO_TOKEN_TEST" is empty`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestNewGeoRefresherAllowsNoTokenEnvironment(t *testing.T) {
	cfg := config.Default()

	refresher, err := newGeoRefresher(nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if refresher == nil {
		t.Fatal("refresher is nil")
	}
}
