package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
	npupdate "github.com/s2005lg/net-probe/internal/update"
)

type releaseInput struct {
	Manifest  npupdate.Manifest `json:"manifest"`
	Signature string            `json:"signature"`
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
	manifestBody, _ := npupdate.ManifestBytes(input.Manifest)
	result, err := s.db.ExecContext(r.Context(), `INSERT INTO agent_releases(manifest_json,signature,version,os,arch,size_bytes,sha256,imported_by_user_id,imported_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		string(manifestBody), input.Signature, input.Manifest.Version, input.Manifest.OS, input.Manifest.Arch,
		input.Manifest.ByteSize, input.Manifest.SHA256, actor.UserID, time.Now().Unix())
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "release_exists"}})
		return
	}
	id, _ := result.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "manifest": input.Manifest, "signature": input.Signature})
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
	payload, _ := json.Marshal(releaseInput{Manifest: manifest, Signature: signatureText})
	commands := make([]controlproto.Command, 0, len(input.AgentIDs))
	for _, agentID := range input.AgentIDs {
		command, err := s.commandStore.Create(r.Context(), actor, agentID, controlproto.Upgrade, payload)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "command_create_failed"}})
			return
		}
		commands = append(commands, command)
		if err := s.commandDispatcher.DispatchAgent(r.Context(), agentID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "command_dispatch_failed"}})
			return
		}
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
