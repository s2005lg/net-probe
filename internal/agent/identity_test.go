package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	panelapi "github.com/s2005lg/net-probe/internal/panel/api"
	panelconfig "github.com/s2005lg/net-probe/internal/panel/config"
	paneldb "github.com/s2005lg/net-probe/internal/panel/db"
	"github.com/s2005lg/net-probe/internal/panel/pki"
)

func TestBootstrapFingerprintMismatchSendsNoEnrollmentSecret(t *testing.T) {
	caPEM, fingerprint := testCA(t)
	var enrollmentRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/ca":
			_, _ = w.Write(caPEM)
		case "/api/v1/agents/enroll":
			enrollmentRequests.Add(1)
			t.Fatal("enrollment secret sent after CA mismatch")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := Enroll(context.Background(), server.Client(), EnrollmentOptions{
		PanelURL:      server.URL,
		CAFingerprint: strings.Repeat("0", len(fingerprint)),
		Code:          "must-not-leave-client",
		PKIDir:        t.TempDir(),
		NodeID:        "node-bootstrap",
		Version:       "v1",
	})
	if err == nil {
		t.Fatal("accepted mismatched CA fingerprint")
	}
	if enrollmentRequests.Load() != 0 {
		t.Fatalf("enrollment requests=%d", enrollmentRequests.Load())
	}
}

type liveEnrollmentPanel struct {
	URL         string
	Fingerprint string
	Code        string
	Manager     *pki.Manager
	DB          *sql.DB
}

func startEnrollmentPanel(t *testing.T) liveEnrollmentPanel {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := listener.Addr().String()
	manager, err := pki.Ensure(t.TempDir(), "https://"+host)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	d, err := paneldb.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	if err := paneldb.Migrate(d); err != nil {
		listener.Close()
		d.Close()
		t.Fatal(err)
	}
	cfg := panelconfig.Default()
	release, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := panelapi.New(d, cfg)
	server.ConfigureAgentPKI(manager, panelapi.StaticReleasePublicKey(release))
	code := "test-enrollment-code-with-256-bits-placeholder"
	hash := sha256.Sum256([]byte(code))
	now := time.Now().Unix()
	if _, err := d.Exec(`INSERT INTO agent_enrollment_tokens(token_hash,created_by_user_id,created_at,expires_at) VALUES(?,?,?,?)`, hex.EncodeToString(hash[:]), 1, now, now+600); err != nil {
		t.Fatal(err)
	}
	httpServer := panelapi.NewTLSServer(host, server.Routes(), manager.TLSConfig())
	httpServer.ErrorLog = log.New(io.Discard, "", 0)
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ServeTLS(listener, manager.ServerCertFile, manager.ServerKeyFile) }()
	t.Cleanup(func() {
		_ = httpServer.Close()
		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			t.Errorf("serve Panel: %v", err)
		}
		_ = d.Close()
	})
	return liveEnrollmentPanel{URL: "https://" + host, Fingerprint: manager.CAFingerprint(), Code: code, Manager: manager, DB: d}
}

func insecureBootstrapClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402 -- test bootstrap client
}

func TestEnrollmentPersistsLoadableIdentityWithPrivateFileModes(t *testing.T) {
	panel := startEnrollmentPanel(t)
	pkiDir := t.TempDir()
	id, err := Enroll(context.Background(), insecureBootstrapClient(), EnrollmentOptions{
		PanelURL: panel.URL, CAFingerprint: panel.Fingerprint, Code: panel.Code,
		PKIDir: pkiDir, NodeID: "node-live-enrollment", Version: "v1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id.AgentID == "" || id.TLSConfig == nil || len(id.CommandKey) != ed25519.PublicKeySize || len(id.ReleaseKey) != ed25519.PublicKeySize {
		t.Fatalf("identity=%+v", id)
	}
	for _, name := range []string{"agent.key", "agent.crt", "ca.crt", "command-signing.pub", "release-signing.pub", "identity.json"} {
		info, err := os.Stat(filepath.Join(pkiDir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %s=%o", name, info.Mode().Perm())
		}
	}
	loaded, err := LoadIdentity(panel.URL, pkiDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AgentID != id.AgentID || !bytes.Equal(loaded.CommandKey, id.CommandKey) || !bytes.Equal(loaded.ReleaseKey, id.ReleaseKey) {
		t.Fatalf("loaded=%+v enrolled=%+v", loaded, id)
	}
	pin := hex.EncodeToString(id.ReleaseKey)
	if err := VerifyReleaseKeyPin(filepath.Join(pkiDir, releaseKeyFile), pin); err != nil {
		t.Fatal(err)
	}
	changedPin := "0" + pin[1:]
	if changedPin == pin {
		changedPin = "1" + pin[1:]
	}
	if err := VerifyReleaseKeyPin(filepath.Join(pkiDir, releaseKeyFile), changedPin); err == nil {
		t.Fatal("accepted release key that differs from embedded pin")
	}
	var nodeID, version string
	if err := panel.DB.QueryRow(`SELECT node_id,agent_version FROM agent_identities WHERE agent_id=?`, id.AgentID).Scan(&nodeID, &version); err != nil {
		t.Fatal(err)
	}
	if nodeID != "node-live-enrollment" || version != "v1.2.3" {
		t.Fatalf("stored node=%q version=%q", nodeID, version)
	}
}

func TestEnrollmentRejectsCrossOriginRedirectBeforeSendingCode(t *testing.T) {
	caPEM, fingerprint := testCA(t)
	var redirected atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer other.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/ca" {
			http.Redirect(w, r, other.URL+"/capture", http.StatusFound)
			return
		}
		_, _ = w.Write(caPEM)
	}))
	defer server.Close()
	_, err := Enroll(context.Background(), server.Client(), EnrollmentOptions{
		PanelURL: server.URL, CAFingerprint: fingerprint, Code: "never-redirect",
		PKIDir: t.TempDir(), NodeID: "node-redirect", Version: "v1",
	})
	if err == nil {
		t.Fatal("accepted cross-origin redirect")
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target requests=%d", redirected.Load())
	}
}

func TestRenewIfNeededAtomicallyReplacesNearExpiryCertificate(t *testing.T) {
	panel := startEnrollmentPanel(t)
	pkiDir := t.TempDir()
	id, err := Enroll(context.Background(), insecureBootstrapClient(), EnrollmentOptions{
		PanelURL: panel.URL, CAFingerprint: panel.Fingerprint, Code: panel.Code,
		PKIDir: pkiDir, NodeID: "node-renew-client", Version: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, id.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	oldPEM, oldSerial, err := panel.Manager.IssueAgent(id.AgentID, id.nodeID, csr, time.Now().Add(-61*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	oldLeaf, err := parseSingleCertificate(oldPEM)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(oldLeaf.Raw)
	if _, err := panel.DB.Exec(`UPDATE agent_identities SET cert_serial=?,cert_fingerprint=?,issued_at=?,expires_at=? WHERE agent_id=?`, oldSerial, hex.EncodeToString(fingerprint[:]), oldLeaf.NotBefore.Unix(), oldLeaf.NotAfter.Unix(), id.AgentID); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(pkiDir, agentCertFile), oldPEM); err != nil {
		t.Fatal(err)
	}
	id, err = LoadIdentity(panel.URL, pkiDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := RenewIfNeeded(context.Background(), id, time.Now()); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIdentity(panel.URL, pkiDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.leaf.SerialNumber.String() == oldSerial || loaded.leaf.NotAfter.Sub(time.Now()) < 89*24*time.Hour {
		t.Fatalf("renewed serial=%s old=%s expiry=%s", loaded.leaf.SerialNumber, oldSerial, loaded.leaf.NotAfter)
	}
	info, err := os.Stat(filepath.Join(pkiDir, agentCertFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("renewed certificate mode=%v", info.Mode().Perm())
	}
}

func testCA(t *testing.T) ([]byte, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "bootstrap-test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), hex.EncodeToString(sum[:])
}
