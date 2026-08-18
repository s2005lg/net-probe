package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/s2005lg/net-probe/internal/panel/auth"
)

func TestRoutes(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	h := s.Routes()

	body := `{"schema_version":"1","node_id":"n1","host":{"hostname":"h","load1":0.1,"load5":0.2,"load15":0.3,"mem_used_pct":12.5,"disk_used_pct":13.5},"services":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/report", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ack":true`) {
		t.Fatalf("report code=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauth nodes code=%d body=%s", rr.Code, rr.Body.String())
	}

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO users(username,password_hash,created_at) VALUES('admin',?,?)`, hash, 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login code=%d body=%s", rr.Code, rr.Body.String())
	}
	cookie := rr.Result().Cookies()
	if len(cookie) == 0 || cookie[0].Name != "panel_session" {
		t.Fatalf("cookies=%+v", cookie)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes", nil)
	req.AddCookie(cookie[0])
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("auth nodes code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestEnsureAdmin(t *testing.T) {
	d, cfg := openTestDB(t)
	if err := EnsureAdmin(d, cfg.Admin.User, "secret"); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM users WHERE username=?`, cfg.Admin.User).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("user count = %d", count)
	}

	var hash string
	if err := d.QueryRow(`SELECT password_hash FROM users WHERE username=?`, cfg.Admin.User).Scan(&hash); err != nil {
		t.Fatalf("query hash: %v", err)
	}
	if !auth.CheckPassword(hash, "secret") {
		t.Fatal("password does not match")
	}

	if err := EnsureAdmin(d, cfg.Admin.User, "secret2"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	var count2 int
	if err := d.QueryRow(`SELECT count(*) FROM users WHERE username=?`, cfg.Admin.User).Scan(&count2); err != nil {
		t.Fatalf("count users after second: %v", err)
	}
	if count2 != 1 {
		t.Fatalf("user count after second = %d", count2)
	}
}
