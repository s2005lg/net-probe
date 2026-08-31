package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/panel/pki"
)

func TestOperatorCreatesOnlyAdvertisedSafeCommandAndViewerReadsHistory(t *testing.T) {
	d, cfg := openTestDB(t)
	server := New(d, cfg)
	manager, err := pki.Ensure(t.TempDir(), "https://panel.example.com:8443")
	if err != nil {
		t.Fatal(err)
	}
	release, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.ConfigureAgentPKI(manager, StaticReleasePublicKey(release)); err != nil {
		t.Fatal(err)
	}
	agent := registerAgent(t, server, manager, authAgentID, "node-command-api", time.Now())
	if _, err := d.Exec(`UPDATE agent_identities SET capabilities_json='["collect_now"]' WHERE agent_id=?`, agent.identity.AgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO users(username,password_hash,created_at,role) VALUES('operator','x',1,'operator'),('viewer','x',1,'viewer')`); err != nil {
		t.Fatal(err)
	}
	var operatorID, viewerID int64
	_ = d.QueryRow(`SELECT id FROM users WHERE username='operator'`).Scan(&operatorID)
	_ = d.QueryRow(`SELECT id FROM users WHERE username='viewer'`).Scan(&viewerID)
	operatorToken, _ := auth.NewSession(d, operatorID, time.Hour)
	viewerToken, _ := auth.NewSession(d, viewerID, time.Hour)

	body := []byte(`{"action":"collect_now","payload":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/agents/"+authAgentID+"/commands", bytes.NewReader(body))
	req.SetPathValue("id", authAgentID)
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: operatorToken})
	rr := httptest.NewRecorder()
	server.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		Command controlproto.Command `json:"command"`
		State   string               `json:"state"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Command.Action != controlproto.CollectNow || created.State != "queued" {
		t.Fatalf("created=%+v", created)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/agents/"+authAgentID+"/commands", bytes.NewReader([]byte(`{"action":"self_check","payload":{"checks":["config"]}}`)))
	req.SetPathValue("id", authAgentID)
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: operatorToken})
	rr = httptest.NewRecorder()
	server.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("unsupported capability status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/agents/"+authAgentID+"/commands", nil)
	req.SetPathValue("id", authAgentID)
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: viewerToken})
	rr = httptest.NewRecorder()
	server.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte(created.Command.CommandID)) {
		t.Fatalf("history status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestOperatorCannotCreateUpgradeCommand(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	if err := fixture.server.ConfigureAgentPKI(fixture.manager, StaticReleasePublicKey(fixture.releaseKey)); err != nil {
		t.Fatal(err)
	}
	registerAgent(t, fixture.server, fixture.manager, authAgentID, "node-upgrade-blocked", time.Now())
	if _, err := fixture.server.db.Exec(`UPDATE agent_identities SET capabilities_json='["upgrade"]' WHERE agent_id=?`, authAgentID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/agents/"+authAgentID+"/commands", bytes.NewReader([]byte(`{"action":"upgrade","payload":{}}`)))
	req.SetPathValue("id", authAgentID)
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: fixture.adminToken})
	rr := httptest.NewRecorder()
	fixture.server.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("upgrade status=%d body=%s", rr.Code, rr.Body.String())
	}
}
