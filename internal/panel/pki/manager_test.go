package pki

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

const testAgentID = "123e4567-e89b-42d3-a456-426614174000"

func TestEnsureCreatesServerIPSanAndSeparateKeys(t *testing.T) {
	m, err := Ensure(t.TempDir(), "https://198.51.100.8:24443")
	if err != nil {
		t.Fatal(err)
	}
	cert := parseLeaf(t, m.ServerCertFile)
	if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "198.51.100.8" {
		t.Fatalf("SAN=%v", cert.IPAddresses)
	}
	if len(cert.DNSNames) != 0 {
		t.Fatalf("DNS SAN=%v", cert.DNSNames)
	}
	if bytes.Equal(read(t, m.CAKeyFile), read(t, m.CommandKeyFile)) {
		t.Fatal("keys reused")
	}
}

func TestEnsureCreatesServerDNSSANAndPrivateKeyModes(t *testing.T) {
	m, err := Ensure(t.TempDir(), "https://panel.example.com:24443")
	if err != nil {
		t.Fatal(err)
	}
	cert := parseLeaf(t, m.ServerCertFile)
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "panel.example.com" {
		t.Fatalf("DNS SAN=%v", cert.DNSNames)
	}
	if len(cert.IPAddresses) != 0 {
		t.Fatalf("IP SAN=%v", cert.IPAddresses)
	}
	for _, path := range []string{m.CAKeyFile, m.ServerKeyFile, m.CommandKeyFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode %s = %o", path, got)
		}
	}
}

func TestEnsureReloadsStableMaterialAndRejectsHostMismatch(t *testing.T) {
	dir := t.TempDir()
	first, err := Ensure(dir, "https://panel.example.com:24443")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		first.CAKeyFile, first.CACertFile,
		first.ServerKeyFile, first.ServerCertFile,
		first.CommandKeyFile, first.CommandPublicKeyFile,
	}
	want := make([][]byte, len(paths))
	for i, path := range paths {
		want[i] = read(t, path)
	}

	second, err := Ensure(dir, "https://panel.example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		if got := read(t, path); !bytes.Equal(got, want[i]) {
			t.Fatalf("material changed on reload: %s", path)
		}
	}
	if second.CAFingerprint() != first.CAFingerprint() {
		t.Fatalf("fingerprint changed: %q != %q", second.CAFingerprint(), first.CAFingerprint())
	}

	if _, err := Ensure(dir, "https://other.example.com:24443"); err == nil {
		t.Fatal("accepted public URL not covered by existing certificate")
	}
}

func TestEnsureRejectsCoherentNonP256CAOnReload(t *testing.T) {
	dir := t.TempDir()
	manager, err := Ensure(dir, "https://panel.example.com:24443")
	if err != nil {
		t.Fatal(err)
	}
	replaceWithP384TrustSet(t, manager, "panel.example.com")

	if _, err := Ensure(dir, "https://panel.example.com:24443"); err == nil {
		t.Fatal("accepted a coherent P-384 CA trust set")
	}
}

func TestEnsureConcurrentInitializationSharesOneTrustSet(t *testing.T) {
	const callers = 32
	dir := t.TempDir()
	start := make(chan struct{})
	type result struct {
		manager *Manager
		err     error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			manager, err := Ensure(dir, "https://panel.example.com:24443")
			results <- result{manager: manager, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	fingerprint := ""
	for result := range results {
		if result.err != nil {
			t.Errorf("concurrent Ensure() failed: %v", result.err)
			continue
		}
		if fingerprint == "" {
			fingerprint = result.manager.CAFingerprint()
		}
		if result.manager.CAFingerprint() != fingerprint {
			t.Errorf("CA fingerprint = %q, want %q", result.manager.CAFingerprint(), fingerprint)
		}
	}
	if fingerprint == "" {
		t.Fatal("no concurrent initializer loaded a trust set")
	}
	final, err := Ensure(dir, "https://panel.example.com:24443")
	if err != nil {
		t.Fatalf("reload final trust set: %v", err)
	}
	if final.CAFingerprint() != fingerprint {
		t.Fatalf("final CA fingerprint = %q, want %q", final.CAFingerprint(), fingerprint)
	}
}

func TestCAFingerprintIsColonFreeCertificateDERSHA256(t *testing.T) {
	m, err := Ensure(t.TempDir(), "https://panel.example.com:24443")
	if err != nil {
		t.Fatal(err)
	}
	cert := parseLeaf(t, m.CACertFile)
	sum := sha256.Sum256(cert.Raw)
	if want := hex.EncodeToString(sum[:]); m.CAFingerprint() != want {
		t.Fatalf("fingerprint = %q, want %q", m.CAFingerprint(), want)
	}
}

func TestTLSConfigRequestsAndVerifiesOptionalClientCertificates(t *testing.T) {
	m, err := Ensure(t.TempDir(), "https://panel.example.com:24443")
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.TLSConfig()
	if cfg.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Fatalf("ClientAuth = %v", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Fatal("ClientCAs is nil")
	}
}

func TestIssueAgentUsesOnlySPIFFEIdentityAndNinetyDayExpiry(t *testing.T) {
	m, err := Ensure(t.TempDir(), "https://panel.example.com:24443")
	if err != nil {
		t.Fatal(err)
	}
	csrDER, publicKey := agentCSR(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	certPEM, serial, err := m.IssueAgent(testAgentID, "node-secret", csrDER, now)
	if err != nil {
		t.Fatal(err)
	}
	cert := parseCertificatePEM(t, certPEM)
	if cert.SerialNumber.String() != serial {
		t.Fatalf("serial = %q, certificate serial = %s", serial, cert.SerialNumber)
	}
	if got := cert.NotAfter.Sub(cert.NotBefore); got != 90*24*time.Hour {
		t.Fatalf("certificate validity = %s", got)
	}
	if got, err := AgentIDFromCertificate(cert); err != nil || got != testAgentID {
		t.Fatalf("AgentIDFromCertificate() = %q, %v", got, err)
	}
	if cert.Subject.String() != "" || len(cert.DNSNames) != 0 || len(cert.IPAddresses) != 0 || len(cert.EmailAddresses) != 0 {
		t.Fatalf("untrusted identity fields copied: subject=%q DNS=%v IP=%v email=%v", cert.Subject, cert.DNSNames, cert.IPAddresses, cert.EmailAddresses)
	}
	issuedKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !issuedKey.Equal(publicKey) {
		t.Fatal("issued certificate does not contain CSR public key")
	}
	if err := cert.CheckSignatureFrom(parseLeaf(t, m.CACertFile)); err != nil {
		t.Fatalf("certificate signature: %v", err)
	}
}

func TestIssueAgentRenewalReplacesSerial(t *testing.T) {
	m, err := Ensure(t.TempDir(), "https://panel.example.com:24443")
	if err != nil {
		t.Fatal(err)
	}
	csrDER, _ := agentCSR(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	_, firstSerial, err := m.IssueAgent(testAgentID, "node-1", csrDER, now)
	if err != nil {
		t.Fatal(err)
	}
	_, renewedSerial, err := m.IssueAgent(testAgentID, "node-1", csrDER, now.Add(60*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if renewedSerial == firstSerial {
		t.Fatalf("renewed serial was reused: %s", renewedSerial)
	}
}

func TestIssueAgentRejectsInvalidUUIDAndInvalidCSRSignature(t *testing.T) {
	m, err := Ensure(t.TempDir(), "https://panel.example.com:24443")
	if err != nil {
		t.Fatal(err)
	}
	csrDER, _ := agentCSR(t)
	now := time.Now()
	if _, _, err := m.IssueAgent("not-a-uuid", "node-1", csrDER, now); err == nil {
		t.Fatal("accepted invalid Agent UUID")
	}
	csrDER[len(csrDER)-1] ^= 0xff
	if _, _, err := m.IssueAgent(testAgentID, "node-1", csrDER, now); err == nil {
		t.Fatal("accepted CSR with invalid signature")
	}
}

func TestAgentIDFromCertificateRejectsNonCanonicalOrAdditionalIdentity(t *testing.T) {
	valid, _ := url.Parse("spiffe://net-probe/agent/" + testAgentID)
	other, _ := url.Parse("spiffe://net-probe/agent/123e4567-e89b-42d3-a456-426614174001")
	tests := []struct {
		name string
		uris []*url.URL
	}{
		{name: "missing"},
		{name: "wrong trust domain", uris: []*url.URL{{Scheme: "spiffe", Host: "other", Path: "/agent/" + testAgentID}}},
		{name: "wrong path", uris: []*url.URL{{Scheme: "spiffe", Host: "net-probe", Path: "/node/" + testAgentID}}},
		{name: "non UUID", uris: []*url.URL{{Scheme: "spiffe", Host: "net-probe", Path: "/agent/not-a-uuid"}}},
		{name: "multiple", uris: []*url.URL{valid, other}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := AgentIDFromCertificate(&x509.Certificate{URIs: tt.uris}); err == nil {
				t.Fatal("accepted invalid Agent URI identity")
			}
		})
	}
}

func agentCSR(t *testing.T) ([]byte, *ecdsa.PublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	untrustedURI, err := url.Parse("spiffe://attacker.example/agent/stolen")
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:        pkix.Name{CommonName: "attacker", Organization: []string{"untrusted"}},
		DNSNames:       []string{"attacker.example"},
		EmailAddresses: []string{"attacker@example.com"},
		URIs:           []*url.URL{untrustedURI},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, &key.PublicKey
}

func replaceWithP384TrustSet(t *testing.T, manager *Manager, host string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(101),
		Subject:               pkix.Name{CommonName: "non-P256 test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(102),
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	writeTestPEM(t, manager.CAKeyFile, "EC PRIVATE KEY", caKeyDER, 0o600)
	writeTestPEM(t, manager.CACertFile, "CERTIFICATE", caDER, 0o644)
	writeTestPEM(t, manager.ServerKeyFile, "EC PRIVATE KEY", serverKeyDER, 0o600)
	writeTestPEM(t, manager.ServerCertFile, "CERTIFICATE", serverDER, 0o644)
}

func writeTestPEM(t *testing.T, path, blockType string, der []byte, mode os.FileMode) {
	t.Helper()
	body := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func parseLeaf(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	return parseCertificatePEM(t, read(t, path))
}

func parseCertificatePEM(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatalf("decode certificate PEM: block=%v trailing=%d", block != nil, len(rest))
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
