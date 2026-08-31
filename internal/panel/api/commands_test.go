package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/panel/pki"
	npupdate "github.com/s2005lg/net-probe/internal/update"
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

func TestAdminImportsSignedReleaseAndCreatesConfirmedExplicitUpgrade(t *testing.T) {
	d, cfg := openTestDB(t)
	manager, err := pki.Ensure(t.TempDir(), "https://panel.example.com:8443")
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := New(d, cfg)
	server.PanelVersion = "v1.2.3"
	if err := server.ConfigureAgentPKI(manager, StaticReleasePublicKey(public)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO users(username,password_hash,created_at,role) VALUES('release-admin','x',?,'admin')`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var adminID int64
	_ = d.QueryRow(`SELECT id FROM users WHERE username='release-admin'`).Scan(&adminID)
	token, err := auth.NewSession(d, adminID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE sessions SET reauthenticated_at=? WHERE token=?`, time.Now().Unix(), token); err != nil {
		t.Fatal(err)
	}
	agent := registerAgent(t, server, manager, authAgentID, "node-release-agent", time.Now())
	if _, err := d.Exec(`UPDATE agent_identities SET capabilities_json='["upgrade"]',agent_version='v1.2.3',os='linux',arch='amd64' WHERE agent_id=?`, agent.identity.AgentID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	signed, err := npupdate.SignManifest(private, npupdate.Manifest{
		Version: "v1.2.4", OS: "linux", Arch: "amd64", ByteSize: 1234,
		SHA256: strings.Repeat("a", 64), ArtifactURL: "https://releases.example.com/v1.2.4/net-probe",
		MinimumPanelVersion: "v1.2.0", ControlVersion: controlproto.Version,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	importBody, _ := json.Marshal(map[string]any{
		"manifest": signed.Manifest, "signature": base64.StdEncoding.EncodeToString(signed.Signature),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/releases", bytes.NewReader(importBody))
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: token})
	rr := httptest.NewRecorder()
	server.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", rr.Code, rr.Body.String())
	}

	upgradeBody, _ := json.Marshal(map[string]any{
		"version": "v1.2.4", "os": "linux", "arch": "amd64", "agent_ids": []string{agent.identity.AgentID}, "confirmed": true,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/upgrades", bytes.NewReader(upgradeBody))
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: token})
	rr = httptest.NewRecorder()
	server.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("upgrade status=%d body=%s", rr.Code, rr.Body.String())
	}
	var action, payload string
	if err := d.QueryRow(`SELECT action,payload FROM agent_commands WHERE agent_id=? ORDER BY sequence DESC LIMIT 1`, agent.identity.AgentID).Scan(&action, &payload); err != nil {
		t.Fatal(err)
	}
	if action != string(controlproto.Upgrade) || !strings.Contains(payload, `"version":"v1.2.4"`) ||
		!strings.Contains(payload, `"panel_version":"v1.2.3"`) || strings.Contains(payload, "private") {
		t.Fatalf("action=%q payload=%s", action, payload)
	}
	if _, err := d.Exec(`UPDATE sessions SET reauthenticated_at=0 WHERE token=?`, token); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/upgrades", bytes.NewReader(upgradeBody))
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: token})
	rr = httptest.NewRecorder()
	server.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("upgrade without reauth status=%d body=%s", rr.Code, rr.Body.String())
	}
}
