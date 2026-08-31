package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	identityStateFile = "identity.json"
	agentKeyFile      = "agent.key"
	agentCertFile     = "agent.crt"
	caBundleFile      = "ca.crt"
	commandKeyFile    = "command-signing.pub"
	releaseKeyFile    = "release-signing.pub"
)

type EnrollmentOptions struct {
	PanelURL      string
	CAFingerprint string
	Code          string
	PKIDir        string
	NodeID        string
	Version       string
}

type Identity struct {
	AgentID    string
	TLSConfig  *tls.Config
	CommandKey ed25519.PublicKey
	ReleaseKey ed25519.PublicKey

	mu          sync.Mutex
	panelURL    string
	pkiDir      string
	nodeID      string
	caCert      *x509.Certificate
	privateKey  *ecdsa.PrivateKey
	leaf        *x509.Certificate
	certificate atomic.Pointer[tls.Certificate]
}

type identityState struct {
	AgentID  string `json:"agent_id"`
	NodeID   string `json:"node_id"`
	PanelURL string `json:"panel_url"`
}

type enrollmentResponse struct {
	AgentID     string `json:"agent_id"`
	Certificate []byte `json:"certificate"`
	CABundle    []byte `json:"ca_bundle"`
	CommandKey  []byte `json:"command_key"`
	ReleaseKey  []byte `json:"release_key"`
}

func Enroll(ctx context.Context, client *http.Client, opts EnrollmentOptions) (*Identity, error) {
	panelURL, err := url.Parse(opts.PanelURL)
	if err != nil || panelURL.Scheme != "https" || panelURL.Hostname() == "" || panelURL.User != nil || panelURL.RawQuery != "" || panelURL.Fragment != "" {
		return nil, errors.New("Panel URL must be a valid HTTPS URL")
	}
	if opts.Code == "" || opts.PKIDir == "" || opts.NodeID == "" {
		return nil, errors.New("enrollment code, PKI directory, and Node ID are required")
	}
	expected, err := hex.DecodeString(strings.ToLower(strings.ReplaceAll(opts.CAFingerprint, ":", "")))
	if err != nil || len(expected) != sha256.Size {
		return nil, errors.New("CA fingerprint must be a SHA-256 digest")
	}
	bootstrapClient := cloneEnrollmentClient(client, panelURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL(panelURL, "/api/v1/ca"), nil)
	if err != nil {
		return nil, err
	}
	resp, err := bootstrapClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Panel CA: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch Panel CA: status %d", resp.StatusCode)
	}
	caPEM, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read Panel CA: %w", err)
	}
	caCert, err := parseSingleCertificate(caPEM)
	if err != nil || !caCert.IsCA {
		return nil, errors.New("Panel CA response is invalid")
	}
	actual := sha256.Sum256(caCert.Raw)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return nil, errors.New("Panel CA fingerprint mismatch")
	}
	if err := verifyBootstrapConnection(resp, caCert, panelURL.Hostname()); err != nil {
		return nil, err
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Agent private key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, privateKey)
	if err != nil {
		return nil, fmt.Errorf("create Agent CSR: %w", err)
	}
	requestBody, err := json.Marshal(map[string]any{
		"code": opts.Code, "csr": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}),
		"node_id": opts.NodeID, "agent_version": opts.Version, "os": runtime.GOOS, "arch": runtime.GOARCH,
	})
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	pinnedClient := &http.Client{
		Timeout:       15 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}},
		CheckRedirect: sameOriginRedirect(panelURL, nil),
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(panelURL, "/api/v1/agents/enroll"), bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = pinnedClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enroll Agent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("enroll Agent: status %d", resp.StatusCode)
	}
	var out enrollmentResponse
	if err := decodeIdentityJSON(io.LimitReader(resp.Body, 128*1024), &out); err != nil {
		return nil, fmt.Errorf("decode enrollment response: %w", err)
	}
	identity, err := validateAndBuildIdentity(opts.PanelURL, opts.PKIDir, opts.NodeID, privateKey, caCert, out)
	if err != nil {
		return nil, err
	}
	if err := persistIdentity(identity, out); err != nil {
		return nil, err
	}
	return identity, nil
}

func LoadIdentity(panelURL, pkiDir string) (*Identity, error) {
	stateBody, err := os.ReadFile(filepath.Join(pkiDir, identityStateFile))
	if err != nil {
		return nil, fmt.Errorf("read Agent identity state: %w", err)
	}
	var state identityState
	if err := decodeIdentityJSON(bytes.NewReader(stateBody), &state); err != nil {
		return nil, fmt.Errorf("decode Agent identity state: %w", err)
	}
	if state.PanelURL != panelURL || !validCanonicalUUID(state.AgentID) || state.NodeID == "" {
		return nil, errors.New("Agent identity state does not match Panel URL")
	}
	keyBody, err := os.ReadFile(filepath.Join(pkiDir, agentKeyFile))
	if err != nil {
		return nil, err
	}
	keyBlock, rest := pem.Decode(keyBody)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid Agent private key PEM")
	}
	privateKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil || privateKey.Curve != elliptic.P256() {
		return nil, errors.New("Agent private key must use ECDSA P-256")
	}
	certBody, err := os.ReadFile(filepath.Join(pkiDir, agentCertFile))
	if err != nil {
		return nil, err
	}
	caBody, err := os.ReadFile(filepath.Join(pkiDir, caBundleFile))
	if err != nil {
		return nil, err
	}
	commandBody, err := os.ReadFile(filepath.Join(pkiDir, commandKeyFile))
	if err != nil {
		return nil, err
	}
	releaseBody, err := os.ReadFile(filepath.Join(pkiDir, releaseKeyFile))
	if err != nil {
		return nil, err
	}
	caCert, err := parseSingleCertificate(caBody)
	if err != nil {
		return nil, err
	}
	return validateAndBuildIdentity(panelURL, pkiDir, state.NodeID, privateKey, caCert, enrollmentResponse{
		AgentID: state.AgentID, Certificate: certBody, CABundle: caBody, CommandKey: commandBody, ReleaseKey: releaseBody,
	})
}

func RenewIfNeeded(ctx context.Context, id *Identity, now time.Time) error {
	if id == nil {
		return errors.New("Agent identity is required")
	}
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.leaf.NotAfter.Sub(now) > 30*24*time.Hour {
		return nil
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, id.privateKey)
	if err != nil {
		return fmt.Errorf("create renewal CSR: %w", err)
	}
	body, err := json.Marshal(map[string][]byte{"csr": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})})
	if err != nil {
		return err
	}
	panelURL, err := url.Parse(id.panelURL)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: id.TLSConfig}, CheckRedirect: sameOriginRedirect(panelURL, nil)}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(panelURL, "/api/v1/agents/renew"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("renew Agent certificate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("renew Agent certificate: status %d", resp.StatusCode)
	}
	var out struct {
		Certificate []byte `json:"certificate"`
	}
	if err := decodeIdentityJSON(io.LimitReader(resp.Body, 64*1024), &out); err != nil {
		return err
	}
	leaf, err := parseSingleCertificate(out.Certificate)
	if err != nil {
		return err
	}
	issuedKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !issuedKey.Equal(&id.privateKey.PublicKey) || leaf.SerialNumber.Cmp(id.leaf.SerialNumber) == 0 {
		return errors.New("renewed certificate identity is invalid")
	}
	if err := verifyAgentCertificate(leaf, id.caCert, id.AgentID); err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(out.Certificate, encodeECPrivateKey(id.privateKey))
	if err != nil {
		return err
	}
	cert.Leaf = leaf
	if err := writeAtomic(filepath.Join(id.pkiDir, agentCertFile), out.Certificate); err != nil {
		return err
	}
	if err := syncDirectory(id.pkiDir); err != nil {
		return err
	}
	id.leaf = leaf
	id.certificate.Store(&cert)
	return nil
}

func cloneEnrollmentClient(client *http.Client, origin *url.URL) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // #nosec G402 -- public CA fetch only; pin and server signature verified before secret use.
		}}}
	}
	cloned := *client
	cloned.CheckRedirect = sameOriginRedirect(origin, client.CheckRedirect)
	return &cloned
}

func sameOriginRedirect(origin *url.URL, prior func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != origin.Scheme || !strings.EqualFold(req.URL.Host, origin.Host) {
			return errors.New("cross-origin enrollment redirect rejected")
		}
		if prior != nil {
			return prior(req, via)
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}
}

func endpointURL(panelURL *url.URL, route string) string {
	endpoint := *panelURL
	endpoint.Path = strings.TrimRight(panelURL.Path, "/") + route
	endpoint.RawPath, endpoint.RawQuery, endpoint.Fragment = "", "", ""
	return endpoint.String()
}

func verifyBootstrapConnection(resp *http.Response, caCert *x509.Certificate, hostname string) error {
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return nil
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	intermediates := x509.NewCertPool()
	for _, cert := range resp.TLS.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, err := resp.TLS.PeerCertificates[0].Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: intermediates, DNSName: hostname, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return fmt.Errorf("verify Panel server with pinned CA: %w", err)
	}
	return nil
}

func validateAndBuildIdentity(panelURL, pkiDir, nodeID string, privateKey *ecdsa.PrivateKey, pinnedCA *x509.Certificate, out enrollmentResponse) (*Identity, error) {
	responseCA, err := parseSingleCertificate(out.CABundle)
	if err != nil || !bytes.Equal(responseCA.Raw, pinnedCA.Raw) {
		return nil, errors.New("enrollment response CA differs from pinned CA")
	}
	leaf, err := parseSingleCertificate(out.Certificate)
	if err != nil {
		return nil, errors.New("enrollment response certificate is invalid")
	}
	issuedKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !issuedKey.Equal(&privateKey.PublicKey) {
		return nil, errors.New("enrollment certificate key does not match local private key")
	}
	if err := verifyAgentCertificate(leaf, responseCA, out.AgentID); err != nil {
		return nil, err
	}
	commandKey, err := parseEd25519PublicKey(out.CommandKey)
	if err != nil {
		return nil, fmt.Errorf("parse command verification key: %w", err)
	}
	releaseKey, err := parseEd25519PublicKey(out.ReleaseKey)
	if err != nil {
		return nil, fmt.Errorf("parse release verification key: %w", err)
	}
	cert, err := tls.X509KeyPair(out.Certificate, encodeECPrivateKey(privateKey))
	if err != nil {
		return nil, fmt.Errorf("load enrolled certificate: %w", err)
	}
	cert.Leaf = leaf
	id := &Identity{AgentID: out.AgentID, CommandKey: commandKey, ReleaseKey: releaseKey, panelURL: panelURL, pkiDir: pkiDir, nodeID: nodeID, caCert: responseCA, privateKey: privateKey, leaf: leaf}
	id.installTLSConfig(&cert)
	return id, nil
}

func verifyAgentCertificate(leaf, caCert *x509.Certificate, agentID string) error {
	if !validCanonicalUUID(agentID) || len(leaf.URIs) != 1 || leaf.URIs[0].String() != "spiffe://net-probe/agent/"+agentID {
		return errors.New("enrollment certificate Agent identity is invalid")
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return fmt.Errorf("verify Agent certificate: %w", err)
	}
	return nil
}

func validCanonicalUUID(value string) bool {
	if len(value) != 36 || value != strings.ToLower(value) || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func parseSingleCertificate(body []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseEd25519PublicKey(body []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid public key PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	public, ok := key.(ed25519.PublicKey)
	if !ok || len(public) != ed25519.PublicKeySize {
		return nil, errors.New("public key is not Ed25519")
	}
	return ed25519.PublicKey(bytes.Clone(public)), nil
}

func VerifyReleaseKeyPin(path, expectedHex string) error {
	expected, err := hex.DecodeString(expectedHex)
	if err != nil || len(expected) != ed25519.PublicKeySize || expectedHex != strings.ToLower(expectedHex) {
		return errors.New("embedded release verification key is invalid")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read release verification key: %w", err)
	}
	actual, err := parseEd25519PublicKey(body)
	if err != nil {
		return fmt.Errorf("parse release verification key: %w", err)
	}
	if subtle.ConstantTimeCompare(expected, actual) != 1 {
		return errors.New("enrolled release verification key does not match Agent binary")
	}
	return nil
}

func encodeECPrivateKey(key *ecdsa.PrivateKey) []byte {
	der, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func (id *Identity) installTLSConfig(cert *tls.Certificate) {
	id.certificate.Store(cert)
	roots := x509.NewCertPool()
	roots.AddCert(id.caCert)
	id.TLSConfig = &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			current := id.certificate.Load()
			if current == nil {
				return nil, errors.New("Agent certificate unavailable")
			}
			return current, nil
		},
	}
}

func persistIdentity(id *Identity, out enrollmentResponse) error {
	if err := os.MkdirAll(id.pkiDir, 0o700); err != nil {
		return fmt.Errorf("create Agent PKI directory: %w", err)
	}
	if err := os.Chmod(id.pkiDir, 0o700); err != nil {
		return fmt.Errorf("secure Agent PKI directory: %w", err)
	}
	state, err := json.Marshal(identityState{AgentID: id.AgentID, NodeID: id.nodeID, PanelURL: id.panelURL})
	if err != nil {
		return err
	}
	files := []struct {
		name string
		body []byte
	}{
		{name: agentKeyFile, body: encodeECPrivateKey(id.privateKey)}, {name: agentCertFile, body: out.Certificate},
		{name: caBundleFile, body: out.CABundle}, {name: commandKeyFile, body: out.CommandKey},
		{name: releaseKeyFile, body: out.ReleaseKey}, {name: identityStateFile, body: state},
	}
	for _, file := range files {
		if err := writeAtomic(filepath.Join(id.pkiDir, file.name), file.body); err != nil {
			return err
		}
	}
	return syncDirectory(id.pkiDir)
}

func writeAtomic(target string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".identity-*")
	if err != nil {
		return fmt.Errorf("create temporary identity file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, target)
}

func syncDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func decodeIdentityJSON(reader io.Reader, out any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON value")
	}
	return nil
}
