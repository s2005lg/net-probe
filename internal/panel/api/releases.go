package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
	npupdate "github.com/s2005lg/net-probe/internal/update"
)

var githubReleaseBase = "https://github.com/s2005lg/net-probe/releases/download"
var githubHTTPClient = http.DefaultClient

type releaseInput struct {
	Manifest  npupdate.Manifest `json:"manifest"`
	Signature string            `json:"signature"`
}

type upgradeReleaseInput struct {
	Manifest     npupdate.Manifest `json:"manifest"`
	Signature    string            `json:"signature"`
	PanelVersion string            `json:"panel_version"`
}

func (s *Server) handleImportRelease(w http.ResponseWriter, r *http.Request) {
	actor, ok := auth.ActorFromContext(r.Context())
	if !ok || s.releaseKey == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "unavailable"}})
		return
	}
	var input releaseInput
	if err := decodeStrictJSON(r.Body, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	public, signature, err := s.releaseVerification(input.Signature)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_signature"}})
		return
	}
	panelVersion := s.PanelVersion
	if panelVersion == "" {
		panelVersion = "v0.0.0"
	}
	if err := npupdate.VerifyManifest(public, npupdate.SignedManifest{Manifest: input.Manifest, Signature: signature}, npupdate.VerifyOptions{
		CurrentVersion: "v0.0.0", PanelVersion: panelVersion, OS: input.Manifest.OS, Arch: input.Manifest.Arch,
		ControlVersion: controlproto.Version, Now: time.Now(),
	}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_release"}})
		return
	}
	id, importedAt, err := s.storeRelease(r.Context(), actor.UserID, input.Manifest, input.Signature)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "release_exists"}})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "manifest": input.Manifest, "signature": input.Signature, "imported_at": importedAt})
}

func (s *Server) handleImportGitHubRelease(w http.ResponseWriter, r *http.Request) {
	actor, ok := auth.ActorFromContext(r.Context())
	if !ok || s.releaseKey == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "unavailable"}})
		return
	}
	var input struct {
		Version string `json:"version"`
		OS      string `json:"os"`
		Arch    string `json:"arch"`
	}
	if err := decodeStrictJSON(r.Body, &input); err != nil || !validReleaseRequest(input.Version, input.OS, input.Arch) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	asset := "net-probe_" + input.OS + "_" + input.Arch
	base := strings.TrimRight(githubReleaseBase, "/") + "/" + input.Version + "/" + asset
	manifestBody, err := fetchReleaseFile(r, base+".manifest.json")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"code": "release_fetch_failed"}})
		return
	}
	manifest, err := npupdate.DecodeManifest(manifestBody)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_release"}})
		return
	}
	signatureBody, err := fetchReleaseFile(r, base+".manifest.sig")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"code": "release_fetch_failed"}})
		return
	}
	signatureText := strings.TrimSpace(string(signatureBody))
	public, signature, err := s.releaseVerification(signatureText)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_signature"}})
		return
	}
	panelVersion := s.PanelVersion
	if panelVersion == "" {
		panelVersion = "v0.0.0"
	}
	if manifest.Version != input.Version || manifest.OS != input.OS || manifest.Arch != input.Arch {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_release"}})
		return
	}
	if err := npupdate.VerifyManifest(public, npupdate.SignedManifest{Manifest: manifest, Signature: signature}, npupdate.VerifyOptions{
		CurrentVersion: "v0.0.0", PanelVersion: panelVersion, OS: input.OS, Arch: input.Arch,
		ControlVersion: controlproto.Version, Now: time.Now(),
	}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_release"}})
		return
	}
	id, importedAt, err := s.storeRelease(r.Context(), actor.UserID, manifest, signatureText)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "release_exists"}})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "manifest": manifest, "signature": signatureText, "imported_at": importedAt})
}

func (s *Server) handleReleases(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,manifest_json,signature,imported_at FROM agent_releases ORDER BY imported_at DESC,id DESC LIMIT 100`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	defer rows.Close()
	type item struct {
		ID         int64             `json:"id"`
		Manifest   npupdate.Manifest `json:"manifest"`
		Signature  string            `json:"signature"`
		ImportedAt int64             `json:"imported_at"`
	}
	items := make([]item, 0)
	for rows.Next() {
		var current item
		var manifestJSON string
		if err := rows.Scan(&current.ID, &manifestJSON, &current.Signature, &current.ImportedAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
			return
		}
		manifest, err := npupdate.DecodeManifest([]byte(manifestJSON))
		if err != nil {
			continue
		}
		current.Manifest = manifest
		items = append(items, current)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateUpgrades(w http.ResponseWriter, r *http.Request) {
	actor, ok := auth.ActorFromContext(r.Context())
	if !ok || auth.RequireRecentReauth(actor, 10*time.Minute) != nil || s.commandStore == nil || s.commandDispatcher == nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{"code": "reauthentication_required"}})
		return
	}
	var input struct {
		Version   string   `json:"version"`
		OS        string   `json:"os"`
		Arch      string   `json:"arch"`
		AgentIDs  []string `json:"agent_ids"`
		Confirmed bool     `json:"confirmed"`
	}
	if err := decodeStrictJSON(r.Body, &input); err != nil || !input.Confirmed || len(input.AgentIDs) == 0 || len(input.AgentIDs) > 100 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "confirmation_required"}})
		return
	}
	var manifestJSON, signatureText string
	if err := s.db.QueryRowContext(r.Context(), `SELECT manifest_json,signature FROM agent_releases WHERE version=? AND os=? AND arch=?`, input.Version, input.OS, input.Arch).
		Scan(&manifestJSON, &signatureText); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "release_not_found"}})
		return
	}
	manifest, err := npupdate.DecodeManifest([]byte(manifestJSON))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "release_invalid"}})
		return
	}
	public, signature, err := s.releaseVerification(signatureText)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "release_invalid"}})
		return
	}
	seen := make(map[string]struct{}, len(input.AgentIDs))
	for _, agentID := range input.AgentIDs {
		if _, duplicate := seen[agentID]; duplicate {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "duplicate_agent"}})
			return
		}
		seen[agentID] = struct{}{}
		var agentVersion, goos, arch, capabilitiesJSON string
		var revokedAt, expiresAt int64
		if err := s.db.QueryRowContext(r.Context(), `SELECT agent_version,os,arch,capabilities_json,revoked_at,expires_at FROM agent_identities WHERE agent_id=?`, agentID).
			Scan(&agentVersion, &goos, &arch, &capabilitiesJSON, &revokedAt, &expiresAt); err != nil || revokedAt != 0 || expiresAt <= time.Now().Unix() {
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "agent_unavailable"}})
			return
		}
		var capabilities []controlproto.Action
		if json.Unmarshal([]byte(capabilitiesJSON), &capabilities) != nil || !containsAction(capabilities, controlproto.Upgrade) || goos != input.OS || arch != input.Arch {
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "agent_incompatible"}})
			return
		}
		if err := npupdate.VerifyManifest(public, npupdate.SignedManifest{Manifest: manifest, Signature: signature}, npupdate.VerifyOptions{
			CurrentVersion: agentVersion, PanelVersion: s.PanelVersion, OS: goos, Arch: arch,
			ControlVersion: controlproto.Version, Now: time.Now(),
		}); err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "agent_incompatible"}})
			return
		}
	}
	payload, _ := json.Marshal(upgradeReleaseInput{Manifest: manifest, Signature: signatureText, PanelVersion: s.PanelVersion})
	commands, err := s.commandStore.CreateBatch(r.Context(), actor, input.AgentIDs, controlproto.Upgrade, payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "command_create_failed"}})
		return
	}
	for _, agentID := range input.AgentIDs {
		// Dispatch is best effort after the atomic commit. Queued commands remain
		// durable and are sent on the Agent's next control-channel reconnect.
		_ = s.commandDispatcher.DispatchAgent(r.Context(), agentID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"commands": commands})
}

func (s *Server) releaseVerification(signatureText string) (ed25519.PublicKey, []byte, error) {
	public, err := s.releaseKey.ReleasePublicKey()
	if err != nil || len(public) != ed25519.PublicKeySize {
		return nil, nil, errors.New("release key unavailable")
	}
	signature, err := base64.StdEncoding.DecodeString(signatureText)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(signature) != signatureText {
		return nil, nil, errors.New("release signature invalid")
	}
	return public, signature, nil
}

func (s *Server) storeRelease(ctx context.Context, userID int64, manifest npupdate.Manifest, signature string) (int64, int64, error) {
	importedAt := time.Now().Unix()
	manifestBody, _ := npupdate.ManifestBytes(manifest)
	result, err := s.db.ExecContext(ctx, `INSERT INTO agent_releases(manifest_json,signature,version,os,arch,size_bytes,sha256,imported_by_user_id,imported_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		string(manifestBody), signature, manifest.Version, manifest.OS, manifest.Arch,
		manifest.ByteSize, manifest.SHA256, userID, importedAt)
	if err != nil {
		return 0, 0, err
	}
	id, err := result.LastInsertId()
	return id, importedAt, err
}

func validReleaseRequest(version, goos, arch string) bool {
	if goos != "linux" || (arch != "amd64" && arch != "arm64") {
		return false
	}
	if len(version) < 6 || version[0] != 'v' {
		return false
	}
	parts := strings.Split(version[1:], ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func fetchReleaseFile(r *http.Request, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := githubHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("release file not found")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 128*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 128*1024 {
		return nil, errors.New("release file is too large")
	}
	return body, nil
}
