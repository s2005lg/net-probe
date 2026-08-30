package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/panel/config"
	"github.com/s2005lg/net-probe/internal/report"
)

type NodeGeoObserver interface {
	ObserveNode(context.Context, string, report.Host) error
}

type Server struct {
	db          *sql.DB
	cfg         *config.Config
	geoObserver NodeGeoObserver
	ConfigPath  string
}

func New(d *sql.DB, cfg *config.Config, observers ...NodeGeoObserver) *Server {
	s := &Server{db: d, cfg: cfg}
	if len(observers) > 0 {
		s.geoObserver = observers[0]
	}
	return s
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/agents/report", s.handleReport)
	mux.HandleFunc("POST /api/v1/admin/login", s.handleLogin)
	mux.Handle("POST /api/v1/admin/logout", s.requireRole(auth.Viewer, s.handleLogout))
	mux.Handle("GET /api/v1/admin/me", s.requireRole(auth.Viewer, s.handleMe))
	mux.Handle("POST /api/v1/admin/reauth", s.requireRole(auth.Viewer, s.handleReauth))

	mux.Handle("GET /api/v1/admin/overview", s.requireRole(auth.Viewer, s.handleOverview))
	mux.Handle("GET /api/v1/admin/nodes", s.requireRole(auth.Viewer, s.handleNodes))
	mux.Handle("GET /api/v1/admin/nodes/{id}", s.requireRole(auth.Viewer, s.handleNodeDetail))
	mux.Handle("PATCH /api/v1/admin/nodes/{id}", s.requireRole(auth.Admin, s.handleNodePatch))
	mux.Handle("DELETE /api/v1/admin/nodes/{id}", s.requireRole(auth.Admin, s.handleNodeDelete))
	mux.Handle("GET /api/v1/admin/nodes/{id}/metrics", s.requireRole(auth.Viewer, s.handleNodeMetrics))
	mux.Handle("GET /api/v1/admin/alerts", s.requireRole(auth.Viewer, s.handleAlerts))
	mux.Handle("POST /api/v1/admin/alerts/{id}/ack", s.requireRole(auth.Admin, s.handleAlertAck))
	mux.Handle("GET /api/v1/admin/tags", s.requireRole(auth.Viewer, s.handleTags))
	mux.Handle("POST /api/v1/admin/tags", s.requireRole(auth.Admin, s.handleTagCreate))
	mux.Handle("DELETE /api/v1/admin/tags/{id}", s.requireRole(auth.Admin, s.handleTagDelete))
	mux.Handle("GET /api/v1/admin/versions", s.requireRole(auth.Viewer, s.handleVersions))
	mux.Handle("PATCH /api/v1/admin/versions/{service_type}", s.requireRole(auth.Admin, s.handleVersionPatch))
	mux.Handle("GET /api/v1/admin/settings", s.requireRole(auth.Viewer, s.handleSettings))
	mux.Handle("PATCH /api/v1/admin/settings", s.requireRole(auth.Admin, s.handleSettingsPatch))
	mux.Handle("/", s.staticHandler())
	return mux
}

func (s *Server) requireRole(role auth.Role, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("panel_session")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
			return
		}
		var actor auth.Actor
		var roleValue string
		var reauthenticatedAt int64
		err = s.db.QueryRow(`SELECT s.user_id,u.username,u.role,s.session_id,s.reauthenticated_at
			FROM sessions s JOIN users u ON u.id=s.user_id
			WHERE s.token=? AND s.expires_at > ?`, cookie.Value, time.Now().Unix()).Scan(&actor.UserID, &actor.Username, &roleValue, &actor.SessionID, &reauthenticatedAt)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
			return
		}
		actor.Role = auth.Role(roleValue)
		if reauthenticatedAt > 0 {
			actor.ReauthenticatedAt = time.Unix(reauthenticatedAt, 0)
		}
		if err := auth.Require(actor, role); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{"code": "forbidden"}})
			return
		}
		next(w, r.WithContext(auth.WithActor(r.Context(), actor)))
	})
}

func EnsureAdmin(d *sql.DB, username, password string) error {
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM users WHERE username=?`, username).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		_, err := d.Exec(`UPDATE users SET role=? WHERE username=?`, auth.Admin, username)
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT INTO users(username,password_hash,created_at,role) VALUES(?,?,?,?)`, username, hash, time.Now().Unix(), auth.Admin)
	return err
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	actor, ok := auth.ActorFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
		return
	}
	reauthenticatedAt := int64(0)
	if !actor.ReauthenticatedAt.IsZero() {
		reauthenticatedAt = actor.ReauthenticatedAt.Unix()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                 actor.UserID,
		"username":           actor.Username,
		"role":               actor.Role,
		"reauthenticated_at": reauthenticatedAt,
	})
}

func (s *Server) handleReauth(w http.ResponseWriter, r *http.Request) {
	actor, ok := auth.ActorFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, actor.UserID).Scan(&hash); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if !auth.CheckPassword(hash, in.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
		return
	}
	result, err := s.db.Exec(`UPDATE sessions SET reauthenticated_at=? WHERE session_id=? AND user_id=?`, time.Now().Unix(), actor.SessionID, actor.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
