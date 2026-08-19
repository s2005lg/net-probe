package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/panel/config"
)

type Server struct {
	db  *sql.DB
	cfg *config.Config
}

func New(d *sql.DB, cfg *config.Config) *Server {
	return &Server{db: d, cfg: cfg}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/agents/report", s.handleReport)
	mux.HandleFunc("POST /api/v1/admin/login", s.handleLogin)
	mux.Handle("POST /api/v1/admin/logout", s.requireAdmin(s.handleLogout))

	mux.Handle("GET /api/v1/admin/overview", s.requireAdmin(s.handleOverview))
	mux.Handle("GET /api/v1/admin/nodes", s.requireAdmin(s.handleNodes))
	mux.Handle("GET /api/v1/admin/nodes/{id}", s.requireAdmin(s.handleNodeDetail))
	mux.Handle("PATCH /api/v1/admin/nodes/{id}", s.requireAdmin(s.handleNodePatch))
	mux.Handle("DELETE /api/v1/admin/nodes/{id}", s.requireAdmin(s.handleNodeDelete))
	mux.Handle("GET /api/v1/admin/nodes/{id}/metrics", s.requireAdmin(s.handleNodeMetrics))
	mux.Handle("GET /api/v1/admin/alerts", s.requireAdmin(s.handleAlerts))
	mux.Handle("POST /api/v1/admin/alerts/{id}/ack", s.requireAdmin(s.handleAlertAck))
	mux.Handle("GET /api/v1/admin/tags", s.requireAdmin(s.handleTags))
	mux.Handle("POST /api/v1/admin/tags", s.requireAdmin(s.handleTagCreate))
	mux.Handle("DELETE /api/v1/admin/tags/{id}", s.requireAdmin(s.handleTagDelete))
	mux.Handle("GET /api/v1/admin/versions", s.requireAdmin(s.handleVersions))
	mux.Handle("PATCH /api/v1/admin/versions/{service_type}", s.requireAdmin(s.handleVersionPatch))
	mux.Handle("GET /api/v1/admin/settings", s.requireAdmin(s.handleSettings))
	mux.Handle("/", s.staticHandler())
	return mux
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("panel_session")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
			return
		}
		var userID int64
		err = s.db.QueryRow(`SELECT user_id FROM sessions WHERE token=? AND expires_at > ?`, cookie.Value, time.Now().Unix()).Scan(&userID)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
			return
		}
		next(w, r)
	})
}

func EnsureAdmin(d *sql.DB, username, password string) error {
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM users WHERE username=?`, username).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT INTO users(username,password_hash,created_at) VALUES(?,?,?)`, username, hash, time.Now().Unix())
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
