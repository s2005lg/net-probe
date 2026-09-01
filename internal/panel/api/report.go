package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/report"
)

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request, identity AgentIdentity) {
	var rep report.Report
	if err := decodeStrictJSON(r.Body, &rep); err != nil || rep.NodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	if rep.NodeID != identity.NodeID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{"code": "node_mismatch"}})
		return
	}

	hostB, _ := json.Marshal(rep.Host)
	svcB, _ := json.Marshal(rep.Services)
	now := time.Now().Unix()

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO nodes(node_id,last_report_at,last_host_json,last_services_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET last_report_at=?,last_host_json=?,last_services_json=?,updated_at=?`,
		rep.NodeID, now, string(hostB), string(svcB), now, now, now, string(hostB), string(svcB), now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if _, err = tx.Exec(`INSERT INTO metrics(node_id,ts,granularity,load1,load5,load15,mem_used_pct,disk_used_pct,services_json)
		VALUES(?,?,'raw',?,?,?,?,?,?)`,
		rep.NodeID, now, rep.Host.Load1, rep.Host.Load5, rep.Host.Load15, rep.Host.MemUsedPct, rep.Host.DiskUsedPct, string(svcB)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if identity.AgentID != "" {
		result, updateErr := tx.Exec(`UPDATE agent_identities SET last_heartbeat_at=?,agent_version=?,updated_at=? WHERE agent_id=? AND cert_serial=? AND revoked_at=0`,
			now, rep.AgentVersion, now, identity.AgentID, identity.Serial)
		if updateErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil || updated != 1 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "identity_not_current"}})
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if s.geoObserver != nil {
		if err := s.geoObserver.ObserveNode(r.Context(), rep.NodeID, rep.Host); err != nil {
			log.Printf("geolocation observation failed for node %q", rep.NodeID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ack":true,"commands":[]}`))
}
