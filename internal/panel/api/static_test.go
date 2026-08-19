package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticServing(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	h := s.Routes()

	for _, path := range []string{"/", "/nodes/abc", "/overview"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("path=%q code=%d body=%s", path, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `<div id="root">`) {
			t.Fatalf("path=%q missing root element: %s", path, rr.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown api code=%d body=%s", rr.Code, rr.Body.String())
	}
}
