package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/report"
)

type nodeRow struct {
	NodeID       string          `json:"node_id"`
	Alias        string          `json:"alias"`
	MutedUntil   int64           `json:"muted_until"`
	LastReportAt int64           `json:"last_report_at"`
	Host         json.RawMessage `json:"host"`
	Services     json.RawMessage `json:"services"`
	Status       string          `json:"status"`
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

func (s *Server) nodeTimeout() time.Duration {
	if d, err := time.ParseDuration(s.cfg.NodeTimeout); err == nil && d > 0 {
		return d
	}
	return 3 * time.Minute
}

func nodeStatus(lastAt int64, timeout time.Duration) string {
	if lastAt == 0 || lastAt < time.Now().Add(-timeout).Unix() {
		return "offline"
	}
	return "online"
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(nodeSelect + ` ORDER BY COALESCE(last_report_at,0) DESC`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	out := make([]nodeRow, 0)
	timeout := s.nodeTimeout()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		n.Status = nodeStatus(n.LastReportAt, timeout)
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
	n.Status = nodeStatus(n.LastReportAt, s.nodeTimeout())
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

type serviceDist struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	timeout := s.nodeTimeout()
	cutoff := time.Now().Add(-timeout).Unix()

	var nodesTotal, nodesOnline, alertsActive int
	if err := s.db.QueryRow(`SELECT count(*) FROM nodes`).Scan(&nodesTotal); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM nodes WHERE COALESCE(last_report_at,0) >= ?`, cutoff).Scan(&nodesOnline); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM alerts WHERE status='firing'`).Scan(&alertsActive); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}

	rows, err := s.db.Query(`SELECT COALESCE(last_services_json,'[]') FROM nodes`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	servicesTotal := 0
	byType := map[string]int{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		var svcs []report.Service
		_ = json.Unmarshal([]byte(raw), &svcs)
		for _, sv := range svcs {
			servicesTotal++
			if sv.Type != "" {
				byType[sv.Type]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}

	dist := make([]serviceDist, 0, len(byType))
	for typ, count := range byType {
		dist = append(dist, serviceDist{Type: typ, Count: count})
	}
	sort.Slice(dist, func(i, j int) bool {
		if dist[i].Count == dist[j].Count {
			return dist[i].Type < dist[j].Type
		}
		return dist[i].Count > dist[j].Count
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes_total":          nodesTotal,
		"nodes_online":         nodesOnline,
		"alerts_active":        alertsActive,
		"services_total":       servicesTotal,
		"service_distribution": dist,
	})
}

type alertRow struct {
	ID             int64  `json:"id"`
	NodeID         string `json:"node_id"`
	Rule           string `json:"rule"`
	Status         string `json:"status"`
	Message        string `json:"message"`
	FirstSeenAt    int64  `json:"first_seen_at"`
	LastSeenAt     int64  `json:"last_seen_at"`
	RecoveredAt    int64  `json:"recovered_at"`
	AcknowledgedAt int64  `json:"acknowledged_at"`
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	q := `SELECT id, node_id, rule, status, message,
		COALESCE(first_seen_at,0), COALESCE(last_seen_at,0), COALESCE(recovered_at,0), COALESCE(acknowledged_at,0)
		FROM alerts`
	args := []any{}
	if status := r.URL.Query().Get("status"); status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY COALESCE(last_seen_at,0) DESC`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	out := make([]alertRow, 0)
	for rows.Next() {
		var a alertRow
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Rule, &a.Status, &a.Message, &a.FirstSeenAt, &a.LastSeenAt, &a.RecoveredAt, &a.AcknowledgedAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAlertAck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	res, err := s.db.Exec(`UPDATE alerts SET status='acknowledged', acknowledged_at=? WHERE id=?`, time.Now().Unix(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleVersionPatch(w http.ResponseWriter, r *http.Request) {
	serviceType := r.PathValue("service_type")
	if serviceType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	var in struct {
		LatestVersion string `json:"latest_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.LatestVersion == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	now := time.Now().Unix()
	if _, err := s.db.Exec(`INSERT INTO versions(service_type,latest_version,source,updated_at)
		VALUES(?,?,'manual',?)
		ON CONFLICT(service_type) DO UPDATE SET latest_version=?,source='manual',updated_at=?`,
		serviceType, in.LatestVersion, now, in.LatestVersion, now); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service_type":   serviceType,
		"latest_version": in.LatestVersion,
		"source":         "manual",
		"updated_at":     now,
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"listen_addr":  s.cfg.ListenAddr,
		"data_dir":     s.cfg.DataDir,
		"node_timeout": s.cfg.NodeTimeout,
		"admin":        map[string]any{"user": s.cfg.Admin.User},
		"retention": map[string]any{
			"raw_days":    s.cfg.Retention.RawDays,
			"hourly_days": s.cfg.Retention.HourlyDays,
			"daily_days":  s.cfg.Retention.DailyDays,
		},
	})
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
