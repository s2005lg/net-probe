package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/report"
)

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+s.cfg.Agent.Token {
		http.Error(w, `{"error":{"code":"unauthorized"}}`, http.StatusUnauthorized)
		return
	}

	var rep report.Report
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil || rep.NodeID == "" {
		http.Error(w, `{"error":{"code":"bad_request"}}`, http.StatusBadRequest)
		return
	}

	hostB, _ := json.Marshal(rep.Host)
	svcB, _ := json.Marshal(rep.Services)
	now := time.Now().Unix()

	_, _ = s.db.Exec(`INSERT INTO nodes(node_id,last_report_at,last_host_json,last_services_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET last_report_at=?,last_host_json=?,last_services_json=?,updated_at=?`,
		rep.NodeID, now, string(hostB), string(svcB), now, now, now, string(hostB), string(svcB), now)
	_, _ = s.db.Exec(`INSERT INTO metrics(node_id,ts,granularity,load1,load5,load15,mem_used_pct,disk_used_pct,services_json)
		VALUES(?,?,'raw',?,?,?,?,?,?)`,
		rep.NodeID, now, rep.Host.Load1, rep.Host.Load5, rep.Host.Load15, rep.Host.MemUsedPct, rep.Host.DiskUsedPct, string(svcB))

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ack":true,"commands":[]}`))
}
