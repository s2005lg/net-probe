package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/pki"
)

const authAgentID = "123e4567-e89b-42d3-a456-426614174010"

type registeredAgent struct {
	identity AgentIdentity
	leaf     *x509.Certificate
	key      *ecdsa.PrivateKey
	certPEM  []byte
}

func configureAgentServer(t *testing.T, s *Server) *pki.Manager {
	t.Helper()
	manager, err := pki.Ensure(t.TempDir(), "https://panel.example.com:8443")
	if err != nil {
		t.Fatal(err)
	}
	release, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureAgentPKI(manager, StaticReleasePublicKey(release))
	return manager
}

func registerAgent(t *testing.T, s *Server, manager *pki.Manager, agentID, nodeID string, issuedAt time.Time) registeredAgent {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, serial, err := manager.IssueAgent(agentID, nodeID, csr, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := parseCertificatePEM(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	now := time.Now().Unix()
	if _, err := s.db.Exec(`INSERT INTO agent_identities(agent_id,node_id,cert_serial,cert_fingerprint,issued_at,expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		agentID, nodeID, serial, hex.EncodeToString(fingerprint[:]), leaf.NotBefore.Unix(), leaf.NotAfter.Unix(), now, now); err != nil {
		t.Fatal(err)
	}
	return registeredAgent{identity: AgentIdentity{AgentID: agentID, NodeID: nodeID, Serial: serial}, leaf: leaf, key: key, certPEM: certPEM}
}

func attachVerifiedAgent(req *http.Request, agent registeredAgent) {
	req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{agent.leaf}}}
}

func TestRequireAgentAcceptsOnlyActiveMatchingCertificate(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	manager := configureAgentServer(t, s)
	agent := registerAgent(t, s, manager, authAgentID, "node-auth", time.Now())
	handler := s.requireAgent(func(w http.ResponseWriter, _ *http.Request, got AgentIdentity) {
		if got != agent.identity {
			t.Fatalf("identity=%+v want=%+v", got, agent.identity)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	attachVerifiedAgent(req, agent)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("active status=%d body=%s", rr.Code, rr.Body.String())
	}

	for _, mutate := range []func(*http.Request){
		func(req *http.Request) { req.TLS = nil },
		func(req *http.Request) {
			req.TLS.VerifiedChains = append(req.TLS.VerifiedChains, req.TLS.VerifiedChains[0])
		},
	} {
		req = httptest.NewRequest(http.MethodGet, "/protected", nil)
		attachVerifiedAgent(req, agent)
		mutate(req)
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("invalid TLS status=%d body=%s", rr.Code, rr.Body.String())
		}
	}

	if _, err := d.Exec(`UPDATE agent_identities SET cert_serial='different' WHERE agent_id=?`, authAgentID); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	attachVerifiedAgent(req, agent)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong serial status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRequireAgentRejectsRevokedAndExpiredDatabaseIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		update string
	}{
		{name: "revoked", update: `UPDATE agent_identities SET revoked_at=1 WHERE agent_id=?`},
		{name: "expired", update: `UPDATE agent_identities SET expires_at=1 WHERE agent_id=?`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, cfg := openTestDB(t)
			s := New(d, cfg)
			manager := configureAgentServer(t, s)
			agent := registerAgent(t, s, manager, authAgentID, "node-auth", time.Now())
			if _, err := d.Exec(test.update, authAgentID); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			attachVerifiedAgent(req, agent)
			rr := httptest.NewRecorder()
			s.requireAgent(func(http.ResponseWriter, *http.Request, AgentIdentity) {
				t.Fatal("revoked/expired identity reached handler")
			}).ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestRenewRotatesSerialOnlyWithinThirtyDays(t *testing.T) {
	d, cfg := openTestDB(t)
	s := New(d, cfg)
	manager := configureAgentServer(t, s)
	agent := registerAgent(t, s, manager, authAgentID, "node-renew", time.Now().Add(-61*24*time.Hour))
	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, newKey)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string][]byte{"csr": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/renew", bytes.NewReader(body))
	attachVerifiedAgent(req, agent)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("renew status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Certificate []byte `json:"certificate"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	renewed, err := parseCertificatePEM(out.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.SerialNumber.String() == agent.identity.Serial || !renewed.PublicKey.(*ecdsa.PublicKey).Equal(&newKey.PublicKey) {
		t.Fatal("renewal did not rotate serial onto requested key")
	}
	var storedSerial string
	if err := d.QueryRow(`SELECT cert_serial FROM agent_identities WHERE agent_id=?`, authAgentID).Scan(&storedSerial); err != nil {
		t.Fatal(err)
	}
	if storedSerial != renewed.SerialNumber.String() {
		t.Fatalf("stored serial=%q renewed=%s", storedSerial, renewed.SerialNumber)
	}

	tooEarly := registerAgent(t, s, manager, "123e4567-e89b-42d3-a456-426614174011", "node-too-early", time.Now())
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/renew", bytes.NewReader(body))
	attachVerifiedAgent(req, tooEarly)
	rr = httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("early renewal status=%d body=%s", rr.Code, rr.Body.String())
	}
}
