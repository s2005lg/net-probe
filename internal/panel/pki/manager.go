package pki

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 397 * 24 * time.Hour
	agentValidity  = 90 * 24 * time.Hour
)

var ensureMu sync.Mutex

// Manager owns the Panel's private CA, HTTPS identity, and independent command
// signing key pair.
type Manager struct {
	PKIDir               string
	CAKeyFile            string
	CACertFile           string
	ServerKeyFile        string
	ServerCertFile       string
	CommandKeyFile       string
	CommandPublicKeyFile string

	caCert        *x509.Certificate
	caKey         *ecdsa.PrivateKey
	clientCAs     *x509.CertPool
	caFingerprint string
}

// Ensure creates the Panel's private PKI once, or validates and reloads it.
// Existing material is never replaced merely because publicURL changed.
func Ensure(dataDir, publicURL string) (*Manager, error) {
	host, err := publicURLHost(publicURL)
	if err != nil {
		return nil, err
	}
	if dataDir == "" {
		return nil, errors.New("data directory is required")
	}

	dir := filepath.Join(dataDir, "pki")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create PKI directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure PKI directory: %w", err)
	}
	m := &Manager{
		PKIDir:               dir,
		CAKeyFile:            filepath.Join(dir, "ca.key"),
		CACertFile:           filepath.Join(dir, "ca.crt"),
		ServerKeyFile:        filepath.Join(dir, "server.key"),
		ServerCertFile:       filepath.Join(dir, "server.crt"),
		CommandKeyFile:       filepath.Join(dir, "command-signing.key"),
		CommandPublicKeyFile: filepath.Join(dir, "command-signing.pub"),
	}
	unlock, err := lockInitialization(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()

	paths := []string{
		m.CAKeyFile, m.CACertFile,
		m.ServerKeyFile, m.ServerCertFile,
		m.CommandKeyFile, m.CommandPublicKeyFile,
	}
	existing := 0
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			existing++
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect PKI material %s: %w", path, err)
		}
	}
	if existing != 0 && existing != len(paths) {
		return nil, errors.New("incomplete PKI material; refusing to replace existing trust files")
	}
	if existing == 0 {
		if err := generate(m, host, time.Now()); err != nil {
			return nil, err
		}
	}
	if err := m.load(host); err != nil {
		return nil, err
	}
	return m, nil
}

func lockInitialization(dir string) (func(), error) {
	ensureMu.Lock()
	lockFile, err := os.OpenFile(filepath.Join(dir, ".init.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		ensureMu.Unlock()
		return nil, fmt.Errorf("open PKI initialization lock: %w", err)
	}
	if err := lockFile.Chmod(0o600); err != nil {
		lockFile.Close()
		ensureMu.Unlock()
		return nil, fmt.Errorf("secure PKI initialization lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		lockFile.Close()
		ensureMu.Unlock()
		return nil, fmt.Errorf("lock PKI initialization: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		ensureMu.Unlock()
	}, nil
}

// TLSConfig returns a server TLS configuration which keeps browser and
// bootstrap access working while verifying any client certificate presented.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.VerifyClientCertIfGiven,
		ClientCAs:  m.clientCAs.Clone(),
	}
}

// CAFingerprint returns the lowercase, colon-free SHA-256 digest of ca.crt's
// DER certificate.
func (m *Manager) CAFingerprint() string {
	return m.caFingerprint
}

// IssueAgent signs a CSR public key with a fresh 90-day Agent certificate.
// CSR subject and SAN fields are deliberately ignored.
func (m *Manager) IssueAgent(agentID, nodeID string, csrDER []byte, now time.Time) ([]byte, string, error) {
	if !validUUID(agentID) {
		return nil, "", errors.New("agent ID must be a canonical UUID")
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, "", fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", fmt.Errorf("verify CSR signature: %w", err)
	}
	publicKey, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		return nil, "", errors.New("CSR must contain an ECDSA P-256 public key")
	}
	identity, err := url.Parse("spiffe://net-probe/agent/" + agentID)
	if err != nil {
		return nil, "", fmt.Errorf("build Agent identity: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, "", err
	}
	notBefore := now.Add(-5 * time.Minute)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    notBefore,
		NotAfter:     notBefore.Add(agentValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{identity},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.caCert, publicKey, m.caKey)
	if err != nil {
		return nil, "", fmt.Errorf("sign Agent certificate: %w", err)
	}
	_ = nodeID // Node binding lives in the Agent record, not the certificate identity.
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), serial.String(), nil
}

// AgentIDFromCertificate returns the UUID from the certificate's sole SPIFFE
// URI identity.
func AgentIDFromCertificate(cert *x509.Certificate) (string, error) {
	if cert == nil || len(cert.URIs) != 1 {
		return "", errors.New("certificate must contain exactly one Agent URI identity")
	}
	u := cert.URIs[0]
	if u == nil || u.Scheme != "spiffe" || u.Host != "net-probe" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("certificate Agent URI identity is invalid")
	}
	const prefix = "/agent/"
	if !strings.HasPrefix(u.Path, prefix) || u.EscapedPath() != u.Path {
		return "", errors.New("certificate Agent URI path is invalid")
	}
	agentID := strings.TrimPrefix(u.Path, prefix)
	if strings.Contains(agentID, "/") || !validUUID(agentID) {
		return "", errors.New("certificate Agent ID is not a canonical UUID")
	}
	return agentID, nil
}

func generate(m *Manager, host string, now time.Time) error {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}
	caSerial, err := randomSerial()
	if err != nil {
		return err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "net-probe Panel CA"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate server key: %w", err)
	}
	serverSerial, err := randomSerial()
	if err != nil {
		return err
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		serverTemplate.IPAddresses = []net.IP{ip}
	} else {
		serverTemplate.DNSNames = []string{host}
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create server certificate: %w", err)
	}

	commandPublic, commandPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate command key: %w", err)
	}
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return fmt.Errorf("marshal server key: %w", err)
	}
	commandKeyDER, err := x509.MarshalPKCS8PrivateKey(commandPrivate)
	if err != nil {
		return fmt.Errorf("marshal command private key: %w", err)
	}
	commandPublicDER, err := x509.MarshalPKIXPublicKey(commandPublic)
	if err != nil {
		return fmt.Errorf("marshal command public key: %w", err)
	}

	files := []struct {
		path string
		mode os.FileMode
		body []byte
	}{
		{m.CAKeyFile, 0o600, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER})},
		{m.CACertFile, 0o644, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})},
		{m.ServerKeyFile, 0o600, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER})},
		{m.ServerCertFile, 0o644, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})},
		{m.CommandKeyFile, 0o600, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: commandKeyDER})},
		{m.CommandPublicKeyFile, 0o644, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: commandPublicDER})},
	}
	for _, file := range files {
		if err := writeExclusive(file.path, file.body, file.mode); err != nil {
			return err
		}
	}
	return syncDir(m.PKIDir)
}

func (m *Manager) load(host string) error {
	for _, path := range []string{m.CAKeyFile, m.ServerKeyFile, m.CommandKeyFile} {
		if err := rejectSymlink(path); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("secure private key %s: %w", path, err)
		}
	}
	for _, path := range []string{m.CACertFile, m.ServerCertFile, m.CommandPublicKeyFile} {
		if err := rejectSymlink(path); err != nil {
			return err
		}
	}

	caCert, err := readCertificate(m.CACertFile)
	if err != nil {
		return err
	}
	caKey, err := readECPrivateKey(m.CAKeyFile)
	if err != nil {
		return err
	}
	caPublicKey, ok := caCert.PublicKey.(*ecdsa.PublicKey)
	if !ok || caPublicKey.Curve != elliptic.P256() {
		return errors.New("CA certificate public key must use ECDSA P-256")
	}
	if caKey.Curve != elliptic.P256() {
		return errors.New("CA private key must use ECDSA P-256")
	}
	if !caCert.IsCA || !caKey.PublicKey.Equal(caCert.PublicKey) {
		return errors.New("CA certificate and private key do not match")
	}
	serverCert, err := readCertificate(m.ServerCertFile)
	if err != nil {
		return err
	}
	serverKey, err := readECPrivateKey(m.ServerKeyFile)
	if err != nil {
		return err
	}
	if !serverKey.PublicKey.Equal(serverCert.PublicKey) {
		return errors.New("server certificate and private key do not match")
	}
	if err := serverCert.VerifyHostname(host); err != nil {
		return fmt.Errorf("existing server certificate does not cover public URL host %q: %w", host, err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := serverCert.Verify(x509.VerifyOptions{Roots: roots, DNSName: host, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return fmt.Errorf("verify server certificate: %w", err)
	}
	if err := validateCommandKeyPair(m.CommandKeyFile, m.CommandPublicKeyFile); err != nil {
		return err
	}
	sum := sha256.Sum256(caCert.Raw)
	m.caCert = caCert
	m.caKey = caKey
	m.clientCAs = roots
	m.caFingerprint = hex.EncodeToString(sum[:])
	return nil
}

func publicURLHost(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("public URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || strings.ToLower(u.Scheme) != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return "", errors.New("public URL must be a valid HTTPS URL with host and port")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("public URL must not contain a path, query, or fragment")
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("public URL has an invalid port")
		}
	}
	return strings.ToLower(u.Hostname()), nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value != strings.ToLower(value) || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	raw := strings.ReplaceAll(value, "-", "")
	if len(raw) != 32 {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		return randomSerial()
	}
	return serial, nil
}

func writeExclusive(path string, body []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pki-*")
	if err != nil {
		return fmt.Errorf("create temporary PKI file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("set PKI file mode: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write PKI file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync PKI file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close PKI file: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("PKI file already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect PKI target: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install PKI file: %w", err)
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open PKI directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync PKI directory: %w", err)
	}
	return nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect PKI file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("PKI file is not a regular file: %s", path)
	}
	return nil
}

func readCertificate(path string) (*x509.Certificate, error) {
	der, err := readPEM(path, "CERTIFICATE")
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse certificate %s: %w", path, err)
	}
	return cert, nil
}

func readECPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	der, err := readPEM(path, "EC PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse EC private key %s: %w", path, err)
	}
	return key, nil
}

func readPEM(path, blockType string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	block, rest := pem.Decode(body)
	if block == nil || block.Type != blockType || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("decode %s PEM block from %s", blockType, path)
	}
	return block.Bytes, nil
}

func validateCommandKeyPair(privatePath, publicPath string) error {
	privateDER, err := readPEM(privatePath, "PRIVATE KEY")
	if err != nil {
		return err
	}
	privateAny, err := x509.ParsePKCS8PrivateKey(privateDER)
	if err != nil {
		return fmt.Errorf("parse command private key: %w", err)
	}
	privateKey, ok := privateAny.(ed25519.PrivateKey)
	if !ok {
		return errors.New("command private key is not Ed25519")
	}
	publicDER, err := readPEM(publicPath, "PUBLIC KEY")
	if err != nil {
		return err
	}
	publicAny, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		return fmt.Errorf("parse command public key: %w", err)
	}
	publicKey, ok := publicAny.(ed25519.PublicKey)
	if !ok || !bytes.Equal(privateKey.Public().(ed25519.PublicKey), publicKey) {
		return errors.New("command public and private keys do not match")
	}
	return nil
}
