package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/report"
)

type nodeRow struct {
	NodeID       string          `json:"node_id"`
	Alias        string          `json:"alias"`
	Tags         []string        `json:"tags"`
	MutedUntil   int64           `json:"muted_until"`
	LastReportAt int64           `json:"last_report_at"`
	Host         json.RawMessage `json:"host"`
	Services     json.RawMessage `json:"services"`
	Status       string          `json:"status"`
	IPLocation   string          `json:"ip_location"`
}

type nodeListResponse struct {
	Items    []nodeRow `json:"items"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}

const nodeSelect = `SELECT node_id, COALESCE(alias,''), COALESCE(muted_until,0), COALESCE(last_report_at,0), COALESCE(last_host_json,'{}'), COALESCE(last_services_json,'[]') FROM nodes`

type metricRow struct {
	TS          int64           `json:"ts"`
	Granularity string          `json:"granularity"`
	Load1       float64         `json:"load1"`
	Load5       float64         `json:"load5"`
	Load15      float64         `json:"load15"`
	MemUsedPct  float64         `json:"mem_used_pct"`
	DiskUsedPct float64         `json:"disk_used_pct"`
	Services    json.RawMessage `json:"services_json"`
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
	q := r.URL.Query()
	page := parseIntDefault(q.Get("page"), 1)
	pageSize := parseIntDefault(q.Get("page_size"), 10)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	where, args := nodeFilter(q, s.nodeTimeout())
	var total int
	if err := s.db.QueryRow(`SELECT count(*) FROM nodes WHERE `+where, args...).Scan(&total); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}

	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := s.db.Query(nodeSelect+` WHERE `+where+` ORDER BY COALESCE(last_report_at,0) DESC LIMIT ? OFFSET ?`, args...)
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
		n.Status = nodeStatus(n.LastReportAt, s.nodeTimeout())
		n.Tags, err = tagsForNode(s.db, n.NodeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		n.IPLocation = s.ipLocationForHost(n.Host)
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, nodeListResponse{Items: out, Total: total, Page: page, PageSize: pageSize})
}

func nodeFilter(q map[string][]string, timeout time.Duration) (string, []any) {
	where := "1=1"
	args := []any{}
	if status := firstQuery(q, "status"); status != "" {
		if status == "online" {
			where += " AND COALESCE(last_report_at,0) >= ?"
			args = append(args, time.Now().Add(-timeout).Unix())
		} else if status == "offline" {
			where += " AND COALESCE(last_report_at,0) < ?"
			args = append(args, time.Now().Add(-timeout).Unix())
		}
	}
	if search := firstQuery(q, "q"); search != "" {
		like := "%" + strings.ToLower(search) + "%"
		where += " AND (LOWER(node_id) LIKE ? OR LOWER(COALESCE(alias,'')) LIKE ? OR LOWER(COALESCE(last_host_json,'{}')) LIKE ?)"
		args = append(args, like, like, like)
	}
	if tag := firstQuery(q, "tag"); tag != "" {
		where += ` AND EXISTS (SELECT 1 FROM node_tags nt JOIN tags t ON t.id=nt.tag_id WHERE nt.node_id=nodes.id AND t.name=?)`
		args = append(args, tag)
	}
	return where, args
}

func firstQuery(q map[string][]string, key string) string {
	if values, ok := q[key]; ok && len(values) > 0 {
		return values[0]
	}
	return ""
}

func parseIntDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func tagsForNode(d *sql.DB, nodeID string) ([]string, error) {
	rows, err := d.Query(`SELECT t.name FROM tags t JOIN node_tags nt ON nt.tag_id=t.id JOIN nodes n ON n.id=nt.node_id WHERE n.node_id=? ORDER BY t.name`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tags = append(tags, name)
	}
	return tags, rows.Err()
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
	n.Tags, err = tagsForNode(s.db, n.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	n.IPLocation = s.ipLocationForHost(n.Host)
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleNodePatch(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	var in struct {
		Alias      *string   `json:"alias"`
		Tags       *[]string `json:"tags"`
		MutedUntil *int64    `json:"muted_until"`
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
	if in.Tags != nil {
		if err := syncNodeTags(s.db, nodeID, *in.Tags); err != nil {
			if err == sql.ErrNoRows {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
			} else {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			}
			return
		}
	}

	row := s.db.QueryRow(nodeSelect+` WHERE node_id=?`, nodeID)
	n, err := scanNode(row)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
		return
	}
	n.Tags, err = tagsForNode(s.db, n.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	n.IPLocation = s.ipLocationForHost(n.Host)
	writeJSON(w, http.StatusOK, n)
}

func syncNodeTags(d *sql.DB, nodeID string, tagNames []string) error {
	var nodePK int64
	if err := d.QueryRow(`SELECT id FROM nodes WHERE node_id=?`, nodeID).Scan(&nodePK); err != nil {
		return err
	}
	if _, err := d.Exec(`DELETE FROM node_tags WHERE node_id=?`, nodePK); err != nil {
		return err
	}
	for _, raw := range tagNames {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, err := d.Exec(`INSERT INTO tags(name) VALUES(?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
			return err
		}
		var tagID int64
		if err := d.QueryRow(`SELECT id FROM tags WHERE name=?`, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := d.Exec(`INSERT OR IGNORE INTO node_tags(node_id,tag_id) VALUES(?,?)`, nodePK, tagID); err != nil {
			return err
		}
	}
	return nil
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
	q := `SELECT ts, granularity, COALESCE(load1,0), COALESCE(load5,0), COALESCE(load15,0), COALESCE(mem_used_pct,0), COALESCE(disk_used_pct,0), COALESCE(services_json,'[]')
		FROM metrics WHERE node_id=? AND (?='' OR granularity=?)`
	args := []any{nodeID, granularity, granularity}
	if from := r.URL.Query().Get("from"); from != "" {
		if ts, err := strconv.ParseInt(from, 10, 64); err == nil {
			q += ` AND ts >= ?`
			args = append(args, ts)
		}
	}
	if to := r.URL.Query().Get("to"); to != "" {
		if ts, err := strconv.ParseInt(to, 10, 64); err == nil {
			q += ` AND ts <= ?`
			args = append(args, ts)
		}
	}
	q += ` ORDER BY ts ASC`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	out := make([]metricRow, 0)
	for rows.Next() {
		var m metricRow
		var servicesRaw string
		if err := rows.Scan(&m.TS, &m.Granularity, &m.Load1, &m.Load5, &m.Load15, &m.MemUsedPct, &m.DiskUsedPct, &servicesRaw); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		m.Services = json.RawMessage(servicesRaw)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type tagRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	NodeCount int    `json:"node_count"`
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT t.id, t.name, COUNT(nt.node_id) FROM tags t LEFT JOIN node_tags nt ON nt.tag_id=t.id GROUP BY t.id ORDER BY t.name`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	out := make([]tagRow, 0)
	for rows.Next() {
		var t tagRow
		if err := rows.Scan(&t.ID, &t.Name, &t.NodeCount); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTagCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	name := strings.TrimSpace(in.Name)
	if _, err := s.db.Exec(`INSERT INTO tags(name) VALUES(?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	var t tagRow
	if err := s.db.QueryRow(`SELECT id, name, 0 FROM tags WHERE name=?`, name).Scan(&t.ID, &t.Name, &t.NodeCount); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTagDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	if _, err := s.db.Exec(`DELETE FROM node_tags WHERE tag_id=?`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	res, err := s.db.Exec(`DELETE FROM tags WHERE id=?`, id)
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

type ipGeoResponse struct {
	Status     string `json:"status"`
	Country    string `json:"country"`
	RegionName string `json:"regionName"`
	City       string `json:"city"`
}

func (s *Server) ipLocationForHost(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var host report.Host
	_ = json.Unmarshal(raw, &host)
	ip := host.IPv4
	if ip == "" {
		ip = host.IPv6
	}
	if ip == "" {
		return ""
	}
	if parsed := net.ParseIP(ip); parsed != nil && parsed.IsPrivate() {
		return "内网"
	}
	return s.ipLocation(ip)
}

func (s *Server) ipLocation(ip string) string {
	s.geoMu.Lock()
	if cached, ok := s.geoCache[ip]; ok {
		s.geoMu.Unlock()
		return cached
	}
	s.geoMu.Unlock()

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://ip-api.com/json/" + ip + "?lang=zh-CN&fields=status,country,regionName,city")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	var geo ipGeoResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&geo); err != nil || geo.Status != "success" {
		return ""
	}
	location := formatIPLocation(geo.Country, geo.RegionName, geo.City)

	s.geoMu.Lock()
	s.geoCache[ip] = location
	s.geoMu.Unlock()
	return location
}

func formatIPLocation(country, regionName, city string) string {
	parts := make([]string, 0, 2)
	if country != "" {
		parts = append(parts, country)
	}
	if city != "" {
		parts = append(parts, city)
	} else if regionName != "" {
		parts = append(parts, regionName)
	}
	return strings.Join(parts, "-")
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
			if sv.Type == "generic" {
				continue
			}
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
	Hostname       string `json:"hostname"`
	Rule           string `json:"rule"`
	Status         string `json:"status"`
	Message        string `json:"message"`
	FirstSeenAt    int64  `json:"first_seen_at"`
	LastSeenAt     int64  `json:"last_seen_at"`
	RecoveredAt    int64  `json:"recovered_at"`
	AcknowledgedAt int64  `json:"acknowledged_at"`
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	q := `SELECT a.id, a.node_id, a.rule, a.status, a.message,
		COALESCE(a.first_seen_at,0), COALESCE(a.last_seen_at,0), COALESCE(a.recovered_at,0), COALESCE(a.acknowledged_at,0),
		COALESCE(n.last_host_json,'{}')
		FROM alerts a LEFT JOIN nodes n ON n.node_id = a.node_id`
	args := []any{}
	conditions := []string{}
	if status := r.URL.Query().Get("status"); status != "" {
		conditions = append(conditions, "a.status=?")
		args = append(args, status)
	}
	if nodeID := r.URL.Query().Get("node_id"); nodeID != "" {
		conditions = append(conditions, "a.node_id=?")
		args = append(args, nodeID)
	}
	if len(conditions) > 0 {
		q += ` WHERE ` + strings.Join(conditions, " AND ")
	}
	q += ` ORDER BY COALESCE(a.last_seen_at,0) DESC`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()

	out := make([]alertRow, 0)
	for rows.Next() {
		var a alertRow
		var hostRaw string
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Rule, &a.Status, &a.Message, &a.FirstSeenAt, &a.LastSeenAt, &a.RecoveredAt, &a.AcknowledgedAt, &hostRaw); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		var host report.Host
		if err := json.Unmarshal([]byte(hostRaw), &host); err == nil {
			a.Hostname = host.Hostname
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
	writeJSON(w, http.StatusOK, s.settingsMap())
}

func (s *Server) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NodeTimeout *string `json:"node_timeout"`
		Retention   *struct {
			RawDays    *int `json:"raw_days"`
			HourlyDays *int `json:"hourly_days"`
			DailyDays  *int `json:"daily_days"`
		} `json:"retention"`
		Alert *struct {
			CertExpiryDays *int    `json:"cert_expiry_days"`
			DiskUsagePct   *int    `json:"disk_usage_pct"`
			MemUsagePct    *int    `json:"mem_usage_pct"`
			TelegramToken  *string `json:"telegram_token"`
			TelegramChatID *string `json:"telegram_chat_id"`
			WebhookURL     *string `json:"webhook_url"`
		} `json:"alert"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}

	if in.NodeTimeout != nil {
		if _, err := time.ParseDuration(*in.NodeTimeout); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request", "message": "invalid node_timeout"}})
			return
		}
		s.cfg.NodeTimeout = *in.NodeTimeout
	}
	if in.Retention != nil {
		if in.Retention.RawDays != nil {
			s.cfg.Retention.RawDays = *in.Retention.RawDays
		}
		if in.Retention.HourlyDays != nil {
			s.cfg.Retention.HourlyDays = *in.Retention.HourlyDays
		}
		if in.Retention.DailyDays != nil {
			s.cfg.Retention.DailyDays = *in.Retention.DailyDays
		}
	}
	if in.Alert != nil {
		if in.Alert.CertExpiryDays != nil {
			s.cfg.Alert.CertExpiryDays = *in.Alert.CertExpiryDays
		}
		if in.Alert.DiskUsagePct != nil {
			s.cfg.Alert.DiskUsagePct = *in.Alert.DiskUsagePct
		}
		if in.Alert.MemUsagePct != nil {
			s.cfg.Alert.MemUsagePct = *in.Alert.MemUsagePct
		}
		if in.Alert.TelegramToken != nil {
			s.cfg.Alert.TelegramToken = *in.Alert.TelegramToken
		}
		if in.Alert.TelegramChatID != nil {
			s.cfg.Alert.TelegramChatID = *in.Alert.TelegramChatID
		}
		if in.Alert.WebhookURL != nil {
			s.cfg.Alert.WebhookURL = *in.Alert.WebhookURL
		}
	}

	if s.ConfigPath != "" {
		f, err := os.Create(s.ConfigPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "config_write_error"}})
			return
		}
		if err := toml.NewEncoder(f).Encode(s.cfg); err != nil {
			_ = f.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "config_write_error"}})
			return
		}
		if err := f.Close(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "config_write_error"}})
			return
		}
	}

	writeJSON(w, http.StatusOK, s.settingsMap())
}

func (s *Server) settingsMap() map[string]any {
	return map[string]any{
		"listen_addr":  s.cfg.ListenAddr,
		"data_dir":     s.cfg.DataDir,
		"node_timeout": s.cfg.NodeTimeout,
		"admin":        map[string]any{"user": s.cfg.Admin.User},
		"retention": map[string]any{
			"raw_days":    s.cfg.Retention.RawDays,
			"hourly_days": s.cfg.Retention.HourlyDays,
			"daily_days":  s.cfg.Retention.DailyDays,
		},
		"alert": map[string]any{
			"cert_expiry_days": s.cfg.Alert.CertExpiryDays,
			"disk_usage_pct":   s.cfg.Alert.DiskUsagePct,
			"mem_usage_pct":    s.cfg.Alert.MemUsagePct,
			"telegram_token":   s.cfg.Alert.TelegramToken,
			"telegram_chat_id": s.cfg.Alert.TelegramChatID,
			"webhook_url":      s.cfg.Alert.WebhookURL,
		},
	}
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
