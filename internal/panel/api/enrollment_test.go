package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/panel/pki"
)

const enrollmentNodeID = "node-enrollment-1"

type enrollmentFixture struct {
	server     *Server
	handler    http.Handler
	manager    *pki.Manager
	adminToken string
	releaseKey ed25519.PublicKey
}

func newEnrollmentFixture(t *testing.T) enrollmentFixture {
	t.Helper()
	d, cfg := openTestDB(t)
	manager, err := pki.Ensure(t.TempDir(), "https://panel.example.com:8443")
	if err != nil {
		t.Fatal(err)
	}
	releaseKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := New(d, cfg)
	s.ConfigureAgentPKI(manager, StaticReleasePublicKey(releaseKey))
	if _, err := d.Exec(`INSERT INTO users(username,password_hash,created_at,role) VALUES('enrollment-admin','x',?,'admin')`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := d.QueryRow(`SELECT id FROM users WHERE username='enrollment-admin'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	adminToken, err := auth.NewSession(d, userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return enrollmentFixture{server: s, handler: s.Routes(), manager: manager, adminToken: adminToken, releaseKey: releaseKey}
}

func createEnrollment(t *testing.T, fixture enrollmentFixture, ttl time.Duration) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"expires_in_seconds": int64(ttl / time.Second), "label": "test node"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/enrollments", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: fixture.adminToken})
	rr := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create enrollment code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Code                string `json:"code"`
		CAFingerprint       string `json:"ca_fingerprint"`
		ReleasePublicKeyHex string `json:"release_public_key_hex"`
		ExpiresAt           int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Code == "" || out.CAFingerprint != fixture.manager.CAFingerprint() ||
		out.ReleasePublicKeyHex != hex.EncodeToString(fixture.releaseKey) || out.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("create response=%+v", out)
	}
	return out.Code
}

func enrollmentCSR(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsaGenerateP256()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	return csr
}

// ecdsaGenerateP256 is kept local so every enrollment test exercises a real,
// independently generated CSR instead of a mocked signer.
func ecdsaGenerateP256() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func enrollmentBody(t *testing.T, code string, csr []byte, nodeID string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"code":          code,
		"csr":           pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}),
		"node_id":       nodeID,
		"agent_version": "v1.2.3",
		"os":            "linux",
		"arch":          "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func postConcurrently(t *testing.T, fixture enrollmentFixture, code string) []int {
	t.Helper()
	csr := enrollmentCSR(t)
	start := make(chan struct{})
	statuses := make([]int, 20)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", bytes.NewReader(enrollmentBody(t, code, csr, enrollmentNodeID)))
			rr := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rr, req)
			statuses[i] = rr.Code
		}(i)
	}
	close(start)
	wg.Wait()
	return statuses
}

func count(statuses []int, status int) int {
	total := 0
	for _, got := range statuses {
		if got == status {
			total++
		}
	}
	return total
}

func TestEnrollmentCodeConsumedOnce(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	code := createEnrollment(t, fixture, 10*time.Minute)
	statuses := postConcurrently(t, fixture, code)
	if count(statuses, http.StatusCreated) != 1 {
		t.Fatalf("statuses=%v", statuses)
	}
}

func TestEnrollmentStoresOnlyCodeHashAndReturnsAllTrustMaterial(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	code := createEnrollment(t, fixture, 10*time.Minute)
	sum := sha256.Sum256([]byte(code))
	var stored string
	if err := fixture.server.db.QueryRow(`SELECT token_hash FROM agent_enrollment_tokens`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != hex.EncodeToString(sum[:]) || strings.Contains(stored, code) {
		t.Fatalf("stored token=%q", stored)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", bytes.NewReader(enrollmentBody(t, code, enrollmentCSR(t), enrollmentNodeID)))
	rr := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out EnrollmentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.AgentID == "" || len(out.Certificate) == 0 || len(out.CABundle) == 0 || len(out.CommandKey) == 0 || len(out.ReleaseKey) == 0 {
		t.Fatalf("incomplete response: %+v", out)
	}
	block, _ := pem.Decode(out.ReleaseKey)
	if block == nil {
		t.Fatal("release key is not PEM")
	}
	keyAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := keyAny.(ed25519.PublicKey); !ok || !bytes.Equal(got, fixture.releaseKey) {
		t.Fatal("release key differs from injected build key")
	}
}

func TestEnrollmentExpiredReplayAndUnknownAreIndistinguishable(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	validCode := createEnrollment(t, fixture, 10*time.Minute)
	expiredCode := createEnrollment(t, fixture, 10*time.Minute)
	sum := sha256.Sum256([]byte(expiredCode))
	if _, err := fixture.server.db.Exec(`UPDATE agent_enrollment_tokens SET expires_at=? WHERE token_hash=?`, time.Now().Add(-time.Minute).Unix(), hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	csr := enrollmentCSR(t)
	request := func(code, nodeID string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", bytes.NewReader(enrollmentBody(t, code, csr, nodeID)))
		rr := httptest.NewRecorder()
		fixture.handler.ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}
	if status, body := request(validCode, "node-replay"); status != http.StatusCreated {
		t.Fatalf("initial use status=%d body=%s", status, body)
	}
	replayedStatus, replayedBody := request(validCode, "node-replay-2")
	expiredStatus, expiredBody := request(expiredCode, "node-expired")
	unknownStatus, unknownBody := request("not-a-real-enrollment-code", "node-unknown")
	if replayedStatus != http.StatusUnauthorized || expiredStatus != replayedStatus || unknownStatus != replayedStatus || replayedBody != expiredBody || replayedBody != unknownBody {
		t.Fatalf("replay=(%d,%q) expired=(%d,%q) unknown=(%d,%q)", replayedStatus, replayedBody, expiredStatus, expiredBody, unknownStatus, unknownBody)
	}
}

func TestEnrollmentRejectsTamperedCSRWithoutConsumingCode(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	code := createEnrollment(t, fixture, 10*time.Minute)
	csr := enrollmentCSR(t)
	csr[len(csr)-1] ^= 0xff
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", bytes.NewReader(enrollmentBody(t, code, csr, "node-tampered")))
	rr := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("tampered CSR code=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", bytes.NewReader(enrollmentBody(t, code, enrollmentCSR(t), "node-after-tamper")))
	rr = httptest.NewRecorder()
	fixture.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("valid retry code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminRevocationImmediatelyBlocksAgentCertificate(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	agent := registerAgent(t, fixture.server, fixture.manager, authAgentID, "node-revoked", time.Now())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/agents/"+authAgentID+"/revoke", nil)
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: fixture.adminToken})
	rr := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/report", strings.NewReader(`{"schema_version":"1","node_id":"node-revoked","host":{},"services":[]}`))
	attachVerifiedAgent(req, agent)
	rr = httptest.NewRecorder()
	fixture.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked report status=%d body=%s", rr.Code, rr.Body.String())
	}
}
