package main

import (
	"testing"

	"github.com/s2005lg/net-probe/internal/panel/config"
)

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
