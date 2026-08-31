package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/auth"
	"github.com/s2005lg/net-probe/internal/panel/pki"
)

const enrollmentLifetime = 10 * time.Minute

// ReleasePublicKeyProvider supplies the release-verification key embedded into
// a Panel build. Task 9 can provide a build-injected implementation; tests can
// inject StaticReleasePublicKey without any production signing key existing.
type ReleasePublicKeyProvider interface {
	ReleasePublicKey() (ed25519.PublicKey, error)
}

type StaticReleasePublicKey []byte

func (k StaticReleasePublicKey) ReleasePublicKey() (ed25519.PublicKey, error) {
	if len(k) != ed25519.PublicKeySize {
		return nil, errors.New("release public key must be Ed25519")
	}
	return ed25519.PublicKey(bytes.Clone(k)), nil
}

// ConfigureAgentPKI enables enrollment and certificate issuance. Keeping the
// release key behind an interface avoids coupling this task to Task 9's build
// injection and release-signing workflow.
func (s *Server) ConfigureAgentPKI(manager *pki.Manager, releaseKey ReleasePublicKeyProvider) {
	s.pkiManager = manager
	s.releaseKey = releaseKey
}

type EnrollmentResponse struct {
	AgentID     string `json:"agent_id"`
	Certificate []byte `json:"certificate"`
	CABundle    []byte `json:"ca_bundle"`
	CommandKey  []byte `json:"command_key"`
	ReleaseKey  []byte `json:"release_key"`
}

type enrollmentRequest struct {
	Code         string `json:"code"`
	CSR          []byte `json:"csr"`
	NodeID       string `json:"node_id"`
	AgentVersion string `json:"agent_version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
}

func (s *Server) handleCA(w http.ResponseWriter, _ *http.Request) {
	if s.pkiManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "unavailable"}})
		return
	}
	body, err := os.ReadFile(s.pkiManager.CACertFile)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "pki_error"}})
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}

func (s *Server) handleCreateEnrollment(w http.ResponseWriter, r *http.Request) {
	if s.pkiManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "unavailable"}})
		return
	}
	var in struct {
		ExpiresInSeconds int64  `json:"expires_in_seconds"`
		Label            string `json:"label"`
	}
	if err := decodeStrictJSON(r.Body, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	if in.ExpiresInSeconds == 0 {
		in.ExpiresInSeconds = int64(enrollmentLifetime / time.Second)
	}
	if in.ExpiresInSeconds < 1 || in.ExpiresInSeconds > int64(enrollmentLifetime/time.Second) || len(in.Label) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	actor, ok := auth.ActorFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "unauthorized"}})
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "random_error"}})
		return
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(code))
	now := time.Now().Unix()
	expiresAt := now + in.ExpiresInSeconds
	if _, err := s.db.Exec(`INSERT INTO agent_enrollment_tokens(token_hash,created_by_user_id,created_at,expires_at,label) VALUES(?,?,?,?,?)`, hex.EncodeToString(hash[:]), actor.UserID, now, expiresAt, in.Label); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"code":           code,
		"expires_at":     expiresAt,
		"ca_fingerprint": s.pkiManager.CAFingerprint(),
	})
}

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if s.pkiManager == nil || s.releaseKey == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "unavailable"}})
		return
	}
	var in enrollmentRequest
	if err := decodeStrictJSON(r.Body, &in); err != nil || !validEnrollmentMetadata(in) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	csrDER, err := parseCSR(in.CSR)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	caBundle, commandKey, releaseKey, err := s.enrollmentTrustMaterial()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "pki_error"}})
		return
	}
	now := time.Now()
	hash := sha256.Sum256([]byte(in.Code))
	tx, err := s.db.BeginTx(r.Context(), &sql.TxOptions{})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), `UPDATE agent_enrollment_tokens SET consumed_at=? WHERE token_hash=? AND consumed_at=0 AND expires_at>?`, now.Unix(), hex.EncodeToString(hash[:]), now.Unix())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	consumed, err := result.RowsAffected()
	if err != nil || consumed != 1 {
		writeEnrollmentFailure(w)
		return
	}
	agentID, err := randomUUID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "random_error"}})
		return
	}
	certificate, serial, err := s.pkiManager.IssueAgent(agentID, in.NodeID, csrDER, now)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	cert, err := parseCertificatePEM(certificate)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "pki_error"}})
		return
	}
	fingerprint := sha256.Sum256(cert.Raw)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO agent_identities(agent_id,node_id,cert_serial,cert_fingerprint,issued_at,expires_at,agent_version,os,arch,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		agentID, in.NodeID, serial, hex.EncodeToString(fingerprint[:]), cert.NotBefore.Unix(), cert.NotAfter.Unix(), in.AgentVersion, in.OS, in.Arch, now.Unix(), now.Unix())
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "identity_conflict"}})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusCreated, EnrollmentResponse{
		AgentID: agentID, Certificate: certificate, CABundle: caBundle, CommandKey: commandKey, ReleaseKey: releaseKey,
	})
}

func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request, identity AgentIdentity) {
	var in struct {
		CSR []byte `json:"csr"`
	}
	if err := decodeStrictJSON(r.Body, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	csrDER, err := parseCSR(in.CSR)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	var expiresAt int64
	if err := s.db.QueryRow(`SELECT expires_at FROM agent_identities WHERE agent_id=? AND cert_serial=? AND revoked_at=0`, identity.AgentID, identity.Serial).Scan(&expiresAt); err != nil {
		writeAgentUnauthorized(w)
		return
	}
	now := time.Now()
	if expiresAt-now.Unix() > int64((30*24*time.Hour)/time.Second) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "renewal_not_due"}})
		return
	}
	certificate, serial, err := s.pkiManager.IssueAgent(identity.AgentID, identity.NodeID, csrDER, now)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	cert, err := parseCertificatePEM(certificate)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "pki_error"}})
		return
	}
	fingerprint := sha256.Sum256(cert.Raw)
	result, err := s.db.Exec(`UPDATE agent_identities SET cert_serial=?,cert_fingerprint=?,issued_at=?,expires_at=?,updated_at=? WHERE agent_id=? AND cert_serial=? AND revoked_at=0`,
		serial, hex.EncodeToString(fingerprint[:]), cert.NotBefore.Unix(), cert.NotAfter.Unix(), now.Unix(), identity.AgentID, identity.Serial)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		writeAgentUnauthorized(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificate": certificate})
}

func (s *Server) handleRevokeAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	now := time.Now().Unix()
	result, err := s.db.Exec(`UPDATE agent_identities SET revoked_at=?,updated_at=? WHERE agent_id=? AND revoked_at=0`, now, now, agentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	updated, err := result.RowsAffected()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	if updated != 1 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
		return
	}
	if s.controlHub != nil {
		s.controlHub.Disconnect(agentID, "revoked")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enrollmentTrustMaterial() ([]byte, []byte, []byte, error) {
	caBundle, err := os.ReadFile(s.pkiManager.CACertFile)
	if err != nil {
		return nil, nil, nil, err
	}
	commandKey, err := os.ReadFile(s.pkiManager.CommandPublicKeyFile)
	if err != nil {
		return nil, nil, nil, err
	}
	releasePublic, err := s.releaseKey.ReleasePublicKey()
	if err != nil {
		return nil, nil, nil, err
	}
	releaseDER, err := x509.MarshalPKIXPublicKey(releasePublic)
	if err != nil {
		return nil, nil, nil, err
	}
	releasePEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: releaseDER})
	return caBundle, commandKey, releasePEM, nil
}

func parseCSR(body []byte) ([]byte, error) {
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, err
	}
	public, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || public.Curve != elliptic.P256() {
		return nil, errors.New("CSR key must use ECDSA P-256")
	}
	return block.Bytes, nil
}

func parseCertificatePEM(body []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func validEnrollmentMetadata(in enrollmentRequest) bool {
	return in.Code != "" && len(in.Code) <= 256 && in.NodeID != "" && len(in.NodeID) <= 256 && len(in.AgentVersion) <= 128 && len(in.OS) <= 64 && len(in.Arch) <= 64 && len(in.CSR) <= 16*1024
}

func decodeStrictJSON(body io.Reader, out any) error {
	decoder := json.NewDecoder(io.LimitReader(body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func randomUUID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:]), nil
}

func writeEnrollmentFailure(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "enrollment_failed"}})
}
