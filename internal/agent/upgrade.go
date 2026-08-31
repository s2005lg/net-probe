package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	npupdate "github.com/s2005lg/net-probe/internal/update"
)

type UpgradeManager struct {
	dir            string
	identity       *Identity
	currentVersion string
	panelVersion   string
	client         *http.Client
	now            func() time.Time
	bootID         string
	proofPath      string
	readinessMu    sync.Mutex
	systemReady    bool
	controlReady   bool
	reportReady    bool
	proofWritten   bool
}

type upgradePayload struct {
	Manifest  npupdate.Manifest `json:"manifest"`
	Signature string            `json:"signature"`
}

func NewUpgradeManager(stateDir string, identity *Identity, currentVersion string, client *http.Client) (*UpgradeManager, error) {
	if stateDir == "" || identity == nil || !validCanonicalUUID(identity.AgentID) || len(identity.ReleaseKey) != ed25519.PublicKeySize || currentVersion == "" {
		return nil, errors.New("upgrade manager requires state, identity, release key, and version")
	}
	dir := filepath.Join(stateDir, "updates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	bootID, err := readBootID()
	if err != nil {
		return nil, err
	}
	runtimeDir := os.Getenv("RUNTIME_DIRECTORY")
	if !filepath.IsAbs(runtimeDir) || filepath.Clean(runtimeDir) == "/" {
		runtimeDir = "/run/net-probe"
	}
	manager := &UpgradeManager{
		dir: dir, identity: identity, currentVersion: currentVersion, client: client, now: time.Now,
		bootID: bootID, proofPath: filepath.Join(runtimeDir, npupdate.UpgradeProofFile),
	}
	_ = os.Remove(manager.proofPath)
	return manager, nil
}

func (m *UpgradeManager) Handle(ctx context.Context, raw json.RawMessage, commandID string) CommandOutcome {
	failure := func(code string) CommandOutcome {
		return CommandOutcome{Code: code, Data: json.RawMessage(`{}`), Failed: true}
	}
	var payload upgradePayload
	if err := controlproto.StrictDecodePayload(raw, &payload); err != nil || !validCanonicalUUID(commandID) {
		return failure("invalid_payload")
	}
	signature, err := base64.StdEncoding.DecodeString(payload.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(signature) != payload.Signature {
		return failure("invalid_release")
	}
	if outcome, handled := m.existingOutcome(ctx, commandID, payload.Manifest.Version); handled {
		return outcome
	}
	if err := npupdate.VerifyManifest(m.identity.ReleaseKey, npupdate.SignedManifest{Manifest: payload.Manifest, Signature: signature}, npupdate.VerifyOptions{
		CurrentVersion: m.currentVersion, PanelVersion: m.panelVersion, OS: runtime.GOOS, Arch: runtime.GOARCH,
		ControlVersion: controlproto.Version, Now: m.now(),
	}); err != nil {
		return failure("invalid_release")
	}
	artifact, err := m.download(ctx, payload.Manifest)
	if err != nil {
		return failure("download_failed")
	}
	if err := npupdate.VerifyManifest(m.identity.ReleaseKey, npupdate.SignedManifest{Manifest: payload.Manifest, Signature: signature}, npupdate.VerifyOptions{
		CurrentVersion: m.currentVersion, PanelVersion: m.panelVersion, OS: runtime.GOOS, Arch: runtime.GOARCH,
		ControlVersion: controlproto.Version, Artifact: artifact, Now: m.now(),
	}); err != nil {
		return failure("artifact_verification_failed")
	}
	basename := "net-probe-" + payload.Manifest.Version + "-" + commandID
	artifactPath := filepath.Join(m.dir, basename)
	if err := writeAtomic(artifactPath, artifact); err != nil {
		return failure("stage_write_failed")
	}
	request := npupdate.UpdateRequest{
		ArtifactBasename: basename, Manifest: payload.Manifest, Signature: payload.Signature,
		PreviousVersion: m.currentVersion, AgentID: m.identity.AgentID, CommandID: commandID,
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		_ = os.Remove(artifactPath)
		return failure("stage_write_failed")
	}
	if err := writeAtomic(filepath.Join(m.dir, npupdate.PendingRequestFile), requestBody); err != nil {
		_ = os.Remove(artifactPath)
		return failure("stage_write_failed")
	}
	if err := syncDirectory(m.dir); err != nil {
		return failure("stage_write_failed")
	}
	return m.waitForHelper(ctx, commandID, payload.Manifest.Version)
}

func (m *UpgradeManager) existingOutcome(ctx context.Context, commandID, version string) (CommandOutcome, bool) {
	if outcome, handled := m.helperOutcome(commandID, version); handled {
		return outcome, true
	}
	pendingPath := filepath.Join(m.dir, npupdate.PendingRequestFile)
	if body, err := os.ReadFile(pendingPath); err == nil {
		request, decodeErr := npupdate.DecodeUpdateRequest(body)
		if decodeErr != nil {
			return CommandOutcome{Code: "pending_request_invalid", Data: json.RawMessage(`{}`), Failed: true}, true
		}
		if request.CommandID == commandID && request.Manifest.Version == version {
			return m.waitForHelper(ctx, commandID, version), true
		}
		return CommandOutcome{Code: "upgrade_busy", Data: json.RawMessage(`{}`), Failed: true}, true
	}
	return CommandOutcome{}, false
}

func (m *UpgradeManager) helperOutcome(commandID, version string) (CommandOutcome, bool) {
	resultPath := filepath.Join(m.dir, npupdate.HelperResultFile)
	if body, err := os.ReadFile(resultPath); err == nil {
		var result npupdate.HelperResult
		if decodeStrictBytes(body, &result) != nil || result.CommandID == "" {
			return CommandOutcome{Code: "helper_result_invalid", Data: json.RawMessage(`{}`), Failed: true}, true
		}
		if result.CommandID == commandID {
			pendingBody, pendingErr := os.ReadFile(filepath.Join(m.dir, npupdate.PendingRequestFile))
			pending, decodeErr := npupdate.DecodeUpdateRequest(pendingBody)
			validCurrent := (result.State == "succeeded" && m.currentVersion == result.Version) ||
				(result.State == "failed" && m.currentVersion == result.PreviousVersion)
			if pendingErr != nil || decodeErr != nil || pending.CommandID != commandID || pending.Manifest.Version != version ||
				result.Version != version || result.PreviousVersion != pending.PreviousVersion || !validCurrent ||
				(result.State != "succeeded" && result.State != "failed") || result.Code == "" || len(result.Code) > 128 {
				return CommandOutcome{Code: "helper_result_invalid", Data: json.RawMessage(`{}`), Failed: true}, true
			}
			data, _ := json.Marshal(map[string]string{"version": result.Version})
			return CommandOutcome{
				Code: result.Code, Data: data, Failed: result.State == "failed",
				AfterPersist: func() { m.cleanupCommand(commandID) },
			}, true
		}
		if m.cleanupCompleted(result.CommandID) {
			_ = os.Remove(resultPath)
		} else {
			return CommandOutcome{Code: "upgrade_busy", Data: json.RawMessage(`{}`), Failed: true}, true
		}
	}
	return CommandOutcome{}, false
}

func (m *UpgradeManager) waitForHelper(ctx context.Context, commandID, version string) CommandOutcome {
	timer := time.NewTimer(125 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if outcome, handled := m.helperOutcome(commandID, version); handled {
			return outcome
		}
		select {
		case <-ctx.Done():
			return CommandOutcome{Code: "upgrade_staged", Data: json.RawMessage(`{}`), Pending: true}
		case <-timer.C:
			return CommandOutcome{Code: "upgrade_staged", Data: json.RawMessage(`{}`), Pending: true}
		case <-ticker.C:
		}
	}
}

func (m *UpgradeManager) cleanupCompleted(commandID string) bool {
	pendingPath := filepath.Join(m.dir, npupdate.PendingRequestFile)
	body, err := os.ReadFile(pendingPath)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	request, err := npupdate.DecodeUpdateRequest(body)
	if err != nil || request.CommandID != commandID || !safeUpgradeBasename(request.ArtifactBasename) {
		return false
	}
	_ = os.Remove(filepath.Join(m.dir, request.ArtifactBasename))
	_ = os.Remove(pendingPath)
	return true
}

func (m *UpgradeManager) cleanupCommand(commandID string) {
	if m.cleanupCompleted(commandID) {
		_ = os.Remove(filepath.Join(m.dir, npupdate.HelperResultFile))
		_ = syncDirectory(m.dir)
	}
}

func (m *UpgradeManager) download(ctx context.Context, manifest npupdate.Manifest) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifest.ArtifactURL, nil)
	if err != nil {
		return nil, err
	}
	client := *m.client
	originalRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !secureDownloadURL(request.URL) {
			return errors.New("insecure release redirect")
		}
		if originalRedirect != nil {
			return originalRedirect(request, via)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !secureDownloadURL(response.Request.URL) || response.ContentLength > npupdate.MaxArtifactBytes {
		return nil, errors.New("release download rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, npupdate.MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > npupdate.MaxArtifactBytes {
		return nil, npupdate.ErrArtifactSize
	}
	return body, nil
}

func secureDownloadURL(value *url.URL) bool {
	return value != nil && value.Scheme == "https" && value.Hostname() != "" && value.User == nil
}

func decodeStrictBytes(body []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func safeUpgradeBasename(value string) bool {
	return value != "" && filepath.Base(value) == value && !strings.ContainsAny(value, `/\\\r\n\x00`)
}

func (m *UpgradeManager) MarkSystemReady()  { m.markReady("system") }
func (m *UpgradeManager) MarkControlReady() { m.markReady("control") }
func (m *UpgradeManager) MarkReportReady()  { m.markReady("report") }

func (m *UpgradeManager) markReady(signal string) {
	if m == nil {
		return
	}
	m.readinessMu.Lock()
	defer m.readinessMu.Unlock()
	switch signal {
	case "system":
		m.systemReady = true
	case "control":
		m.controlReady = true
	case "report":
		m.reportReady = true
	default:
		return
	}
	if m.proofWritten || !m.systemReady || !m.controlReady || !m.reportReady {
		return
	}
	pendingBody, err := os.ReadFile(filepath.Join(m.dir, npupdate.PendingRequestFile))
	if err != nil {
		return
	}
	request, err := npupdate.DecodeUpdateRequest(pendingBody)
	if err != nil || request.AgentID != m.identity.AgentID || request.Manifest.Version != m.currentVersion {
		return
	}
	proofBody, err := json.Marshal(npupdate.UpgradeProof{
		Version: m.currentVersion, AgentID: m.identity.AgentID, CommandID: request.CommandID, BootID: m.bootID,
	})
	if err != nil {
		return
	}
	if err := writeAtomic(m.proofPath, proofBody); err != nil {
		return
	}
	m.proofWritten = true
}
