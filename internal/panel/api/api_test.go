package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/panel/config"
	"github.com/s2005lg/net-probe/internal/panel/db"
)

func openTestDB(t *testing.T) (*sql.DB, *config.Config) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	cfg := config.Default()
	cfg.Agent.Token = "tok"
	return d, cfg
}

func TestRoleAwareRoutesAndReauth(t *testing.T) {
	d, cfg := openTestDB(t)
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO users(username,password_hash,created_at,role) VALUES('viewer',?,?,'viewer'),('admin',?,?,'admin')`, hash, time.Now().Unix(), hash, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var viewerID, adminID int64
	if err := d.QueryRow(`SELECT id FROM users WHERE username='viewer'`).Scan(&viewerID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT id FROM users WHERE username='admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	viewerToken, err := auth.NewSession(d, viewerID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := auth.NewSession(d, adminID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	otherAdminToken, err := auth.NewSession(d, adminID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := New(d, cfg).Routes()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/me", nil)
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: viewerToken})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"role":"viewer"`) {
		t.Fatalf("viewer me code=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/nodes/node-1", strings.NewReader(`{"alias":"blocked"}`))
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: viewerToken})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer mutation code=%d body=%s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/reauth", strings.NewReader(`{"password":"wrong"}`))
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: viewerToken})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password reauth code=%d body=%s", rr.Code, rr.Body.String())
	}
	var viewerReauthenticatedAt int64
	if err := d.QueryRow(`SELECT reauthenticated_at FROM sessions WHERE token=?`, viewerToken).Scan(&viewerReauthenticatedAt); err != nil {
		t.Fatal(err)
	}
	if viewerReauthenticatedAt != 0 {
		t.Fatalf("wrong-password reauth updated session: %d", viewerReauthenticatedAt)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/reauth", strings.NewReader(`{"password":"secret"}`))
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: adminToken})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reauth code=%d body=%s", rr.Code, rr.Body.String())
	}
	var reauthenticatedAt, otherReauthenticatedAt int64
	if err := d.QueryRow(`SELECT reauthenticated_at FROM sessions WHERE token=?`, adminToken).Scan(&reauthenticatedAt); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT reauthenticated_at FROM sessions WHERE token=?`, otherAdminToken).Scan(&otherReauthenticatedAt); err != nil {
		t.Fatal(err)
	}
	if reauthenticatedAt == 0 || otherReauthenticatedAt != 0 {
		t.Fatalf("reauthenticated_at=%d other=%d", reauthenticatedAt, otherReauthenticatedAt)
	}
}
