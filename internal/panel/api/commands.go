package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
)

func (s *Server) handleCreateCommand(w http.ResponseWriter, r *http.Request) {
	if s.commandStore == nil || s.commandDispatcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "unavailable"}})
		return
	}
	agentID := r.PathValue("id")
	actor, ok := auth.ActorFromContext(r.Context())
	if !ok || agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	var input struct {
		Action  controlproto.Action `json:"action"`
		Payload json.RawMessage     `json:"payload"`
	}
	if err := decodeStrictJSON(r.Body, &input); err != nil || input.Action == controlproto.Upgrade ||
		(input.Action != controlproto.CollectNow && input.Action != controlproto.ReloadConfig && input.Action != controlproto.SelfCheck) ||
		len(input.Payload) == 0 || len(input.Payload) > 16*1024 || !json.Valid(input.Payload) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	if (input.Action == controlproto.CollectNow || input.Action == controlproto.ReloadConfig) && !emptyJSONObject(input.Payload) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_payload"}})
		return
	}
	var capabilitiesJSON string
	var revokedAt, expiresAt int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT capabilities_json,revoked_at,expires_at FROM agent_identities WHERE agent_id=?`, agentID).
		Scan(&capabilitiesJSON, &revokedAt, &expiresAt); err != nil || revokedAt != 0 || expiresAt <= time.Now().Unix() {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found"}})
		return
	}
	var capabilities []controlproto.Action
	if json.Unmarshal([]byte(capabilitiesJSON), &capabilities) != nil || !containsAction(capabilities, input.Action) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "unsupported_capability"}})
		return
	}
	command, err := s.commandStore.Create(r.Context(), actor, agentID, input.Action, input.Payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "command_create_failed"}})
		return
	}
	if err := s.commandDispatcher.DispatchAgent(r.Context(), agentID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "command_dispatch_failed"}})
		return
	}
	state := "queued"
	_ = s.db.QueryRowContext(r.Context(), `SELECT state FROM agent_commands WHERE command_id=?`, command.CommandID).Scan(&state)
	writeJSON(w, http.StatusCreated, map[string]any{"command": command, "state": state})
}

func (s *Server) handleCommandHistory(w http.ResponseWriter, r *http.Request) {
	if s.commandStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "unavailable"}})
		return
	}
	agentID := r.PathValue("id")
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "bad_request"}})
		return
	}
	items, err := s.commandStore.History(r.Context(), agentID, 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "db_error"}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func emptyJSONObject(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var value struct{}
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

func containsAction(actions []controlproto.Action, target controlproto.Action) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}
