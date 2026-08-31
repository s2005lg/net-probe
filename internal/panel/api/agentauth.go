package api

import (
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/pki"
)

type AgentIdentity struct {
	AgentID string
	NodeID  string
	Serial  string
}

func (s *Server) requireAgent(next func(http.ResponseWriter, *http.Request, AgentIdentity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) != 1 || len(r.TLS.VerifiedChains[0]) == 0 {
			writeAgentUnauthorized(w)
			return
		}
		leaf := r.TLS.VerifiedChains[0][0]
		now := time.Now()
		if leaf == nil || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
			writeAgentUnauthorized(w)
			return
		}
		agentID, err := pki.AgentIDFromCertificate(leaf)
		if err != nil {
			writeAgentUnauthorized(w)
			return
		}
		identity := AgentIdentity{AgentID: agentID, Serial: leaf.SerialNumber.String()}
		var revokedAt, expiresAt int64
		err = s.db.QueryRow(`SELECT node_id,revoked_at,expires_at FROM agent_identities WHERE agent_id=? AND cert_serial=?`, identity.AgentID, identity.Serial).Scan(&identity.NodeID, &revokedAt, &expiresAt)
		if err != nil || revokedAt != 0 || expiresAt <= now.Unix() {
			writeAgentUnauthorized(w)
			return
		}
		next(w, r, identity)
	})
}

func writeAgentUnauthorized(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
}
