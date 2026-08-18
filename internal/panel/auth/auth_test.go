package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/db"
)

func TestPassword(t *testing.T) {
	h, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !CheckPassword(h, "secret") || CheckPassword(h, "wrong") {
		t.Fatal("password check failed")
	}
}

func TestNewSession(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	token, err := NewSession(d, 7, time.Hour)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("token length = %d", len(token))
	}

	var userID int64
	if err := d.QueryRow(`SELECT user_id FROM sessions WHERE token=?`, token).Scan(&userID); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if userID != 7 {
		t.Fatalf("user id = %d", userID)
	}
}
