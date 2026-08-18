package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/auth"
)

type nodeRow struct {
	NodeID       string          `json:"node_id"`
	Alias        string          `json:"alias"`
	MutedUntil   int64           `json:"muted_until"`
	LastReportAt int64           `json:"last_report_at"`
	Host         json.RawMessage `json:"host"`
	Services     json.RawMessage `json:"services"`
}

const nodeSelect = `SELECT node_id, COALESCE(alias,''), COALESCE(muted_until,0), COALESCE(last_report_at,0), COALESCE(last_host_json,'{}'), COALESCE(last_services_json,'[]') FROM nodes`

type metricRow struct {
	TS          int64   `json:"ts"`
	Granularity string  `json:"granularity"`
	Load1       float64 `json:"load1"`
	Load5       float64 `json:"load5"`
	Load15      float64 `json:"load15"`
	MemUsedPct  float64 `json:"mem_used_pct"`
	DiskUsedPct float64 `json:"disk_used_pct"`
}

func scanNode(scanner interface{ Scan(...any) error }) (nodeRow, error) {
	var n nodeRow
	var host, services string
	err := scanner.Scan(&n.NodeID, &n.Alias, &n.MutedUntil, &n.LastReportAt, &host, &services)
	n.Host = json.RawMessage(host)
	n.Services = json.RawMessage(services)
	return n, err
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(nodeSelect + ` ORDER BY COALESCE(last_report_at,0) DESC`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	out := make([]nodeRow, 0)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleNodeDetail(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	row := s.db.QueryRow(nodeSelect+` WHERE node_id=?`, nodeID)
	n, err := scanNode(row)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleNodePatch(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	var in struct {
		Alias      *string `json:"alias"`
		MutedUntil *int64  `json:"muted_until"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}

	if in.Alias != nil {
		res, err := s.db.Exec(`UPDATE nodes SET alias=?,updated_at=unixepoch() WHERE node_id=?`, *in.Alias, nodeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
			return
		}
	}
	if in.MutedUntil != nil {
		res, err := s.db.Exec(`UPDATE nodes SET muted_until=?,updated_at=unixepoch() WHERE node_id=?`, *in.MutedUntil, nodeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
			return
		}
	}

	row := s.db.QueryRow(nodeSelect+` WHERE node_id=?`, nodeID)
	n, err := scanNode(row)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}

	for _, stmt := range []string{
		`DELETE FROM node_tags WHERE node_id IN (SELECT id FROM nodes WHERE node_id=?)`,
		`DELETE FROM metrics WHERE node_id=?`,
		`DELETE FROM alerts WHERE node_id=?`,
	} {
		if _, err := s.db.Exec(stmt, nodeID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
	}

	res, err := s.db.Exec(`DELETE FROM nodes WHERE node_id=?`, nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleNodeMetrics(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	granularity := r.URL.Query().Get("granularity")
	rows, err := s.db.Query(`SELECT ts, granularity, COALESCE(load1,0), COALESCE(load5,0), COALESCE(load15,0), COALESCE(mem_used_pct,0), COALESCE(disk_used_pct,0)
		FROM metrics WHERE node_id=? AND (?='' OR granularity=?) ORDER BY ts ASC`, nodeID, granularity, granularity)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	out := make([]metricRow, 0)
	for rows.Next() {
		var m metricRow
		if err := rows.Scan(&m.TS, &m.Granularity, &m.Load1, &m.Load5, &m.Load15, &m.MemUsedPct, &m.DiskUsedPct); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type versionRow struct {
	ServiceType   string `json:"service_type"`
	LatestVersion string `json:"latest_version"`
	Source        string `json:"source"`
	UpdatedAt     int64  `json:"updated_at"`
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT service_type, latest_version, source, updated_at FROM versions ORDER BY service_type`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	out := make([]versionRow, 0)
	for rows.Next() {
		var v versionRow
		if err := rows.Scan(&v.ServiceType, &v.LatestVersion, &v.Source, &v.UpdatedAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Username == "" || in.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}

	var userID int64
	var hash string
	err := s.db.QueryRow(`SELECT id,password_hash FROM users WHERE username=?`, in.Username).Scan(&userID, &hash)
	if err == sql.ErrNoRows || !auth.CheckPassword(hash, in.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}

	token, err := auth.NewSession(s.db, userID, 7*24*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "panel_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("panel_session"); err == nil {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token=?`, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "panel_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
