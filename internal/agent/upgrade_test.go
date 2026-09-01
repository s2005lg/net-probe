package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	npupdate "github.com/s2005lg/net-probe/internal/update"
)

func signedUpgradePayload(t *testing.T, private ed25519.PrivateKey, artifact []byte, artifactURL string) json.RawMessage {
	t.Helper()
	digest := sha256.Sum256(artifact)
	now := time.Now().UTC().Truncate(time.Second)
	signed, err := npupdate.SignManifest(private, npupdate.Manifest{
		Version: "v1.2.4", OS: runtime.GOOS, Arch: runtime.GOARCH, ByteSize: int64(len(artifact)),
		SHA256: hex.EncodeToString(digest[:]), ArtifactURL: artifactURL, MinimumPanelVersion: "v1.2.0",
		ControlVersion: "1", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		Manifest     npupdate.Manifest `json:"manifest"`
		Signature    string            `json:"signature"`
		PanelVersion string            `json:"panel_version"`
	}{Manifest: signed.Manifest, Signature: base64.StdEncoding.EncodeToString(signed.Signature), PanelVersion: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func handleUpgrade(manager *UpgradeManager, ctx context.Context, payload json.RawMessage, commandID string) CommandOutcome {
	command := controlproto.Command{Action: controlproto.Upgrade, CommandID: commandID, Payload: append(json.RawMessage(nil), payload...)}
	return manager.Handle(context.WithValue(ctx, commandEnvelopeContextKey{}, command), payload, commandID)
}

func TestUpgradeManagerStagesVerifiedArtifactAndReturnsDurableHelperResult(t *testing.T) {
	artifact := []byte("new-agent-binary")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(artifact) }))
	defer server.Close()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	manager, err := NewUpgradeManager(stateDir, &Identity{AgentID: executorAgentID, ReleaseKey: public}, "v1.2.3", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	manager.SetPanelVersion("v1.2.3")
	commandID := "123e4567-e89b-42d3-a456-426614174041"
	payload := signedUpgradePayload(t, private, artifact, server.URL+"/net-probe")
	stageContext, cancelStage := context.WithTimeout(context.Background(), 100*time.Millisecond)
	outcome := handleUpgrade(manager, stageContext, payload, commandID)
	cancelStage()
	if !outcome.Pending || outcome.Failed || outcome.Code != "upgrade_staged" {
		t.Fatalf("staged outcome=%+v", outcome)
	}
	pendingBody, err := os.ReadFile(filepath.Join(stateDir, "updates", npupdate.PendingRequestFile))
	if err != nil {
		t.Fatal(err)
	}
	request, err := npupdate.DecodeUpdateRequest(pendingBody)
	if err != nil {
		t.Fatal(err)
	}
	if request.CommandID != commandID || request.PreviousVersion != "v1.2.3" {
		t.Fatalf("request=%+v", request)
	}
	info, err := os.Stat(filepath.Join(stateDir, "updates", request.ArtifactBasename))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode=%v err=%v", info, err)
	}

	upgradedManager, err := NewUpgradeManager(stateDir, &Identity{AgentID: executorAgentID, ReleaseKey: public}, "v1.2.4", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	upgradedManager.SetPanelVersion("v1.2.3")
	completed := make(chan CommandOutcome, 1)
	go func() {
		completed <- handleUpgrade(upgradedManager, context.Background(), payload, commandID)
	}()
	time.Sleep(30 * time.Millisecond)
	resultBody, _ := json.Marshal(npupdate.HelperResult{
		CommandID: commandID, State: "succeeded", Code: "upgrade_applied", Version: "v1.2.4", PreviousVersion: "v1.2.3",
	})
	if err := writeAtomic(filepath.Join(stateDir, "updates", npupdate.HelperResultFile), resultBody); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome = <-completed:
	case <-time.After(time.Second):
		t.Fatal("upgraded Agent did not consume helper result")
	}
	if outcome.Pending || outcome.Failed || outcome.Code != "upgrade_applied" {
		t.Fatalf("completed outcome=%+v", outcome)
	}
}

func TestUpgradeManagerRejectsInsecureRedirect(t *testing.T) {
	artifact := []byte("new-agent-binary")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/net-probe", http.StatusFound)
	}))
	defer server.Close()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewUpgradeManager(t.TempDir(), &Identity{AgentID: executorAgentID, ReleaseKey: public}, "v1.2.3", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	manager.SetPanelVersion("v1.2.3")
	outcome := handleUpgrade(manager, context.Background(), signedUpgradePayload(t, private, artifact, server.URL+"/net-probe"), "123e4567-e89b-42d3-a456-426614174041")
	if !outcome.Failed || outcome.Pending || outcome.Code != "download_failed" {
		t.Fatalf("outcome=%+v", outcome)
	}
}

func TestUpgradeManagerDoesNotChmodRootManagedDirectory(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NET_PROBE_UPDATE_DIRECTORY", dir)
	if _, err := NewUpgradeManager(t.TempDir(), &Identity{AgentID: executorAgentID, ReleaseKey: public}, "v1.2.3", nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o770 {
		t.Fatalf("root-managed directory mode changed to %o", info.Mode().Perm())
	}
}

func TestUpgradeManagerRejectsPanelVersionNotBoundToWelcome(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewUpgradeManager(t.TempDir(), &Identity{AgentID: executorAgentID, ReleaseKey: public}, "v1.2.3", nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetPanelVersion("v1.2.2")
	outcome := handleUpgrade(manager, context.Background(), signedUpgradePayload(t, private, []byte("artifact"), "https://releases.example.test/net-probe"),
		"123e4567-e89b-42d3-a456-426614174041")
	if !outcome.Failed || outcome.Code != "panel_version_mismatch" {
		t.Fatalf("outcome=%+v", outcome)
	}
}

func TestUpgradeReadinessProofRequiresSystemControlAndPanelReport(t *testing.T) {
	artifact := []byte("new-agent-binary")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(artifact) }))
	defer server.Close()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	runtimeDir := t.TempDir()
	t.Setenv("RUNTIME_DIRECTORY", runtimeDir)
	oldManager, err := NewUpgradeManager(stateDir, &Identity{AgentID: executorAgentID, ReleaseKey: public}, "v1.2.3", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	oldManager.SetPanelVersion("v1.2.3")
	commandID := "123e4567-e89b-42d3-a456-426614174041"
	stageContext, cancelStage := context.WithTimeout(context.Background(), 100*time.Millisecond)
	if outcome := handleUpgrade(oldManager, stageContext, signedUpgradePayload(t, private, artifact, server.URL+"/net-probe"), commandID); !outcome.Pending {
		t.Fatalf("staging=%+v", outcome)
	}
	cancelStage()
	manager, err := NewUpgradeManager(stateDir, &Identity{AgentID: executorAgentID, ReleaseKey: public}, "v1.2.4", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	manager.SetPanelVersion("v1.2.3")
	manager.MarkSystemReady()
	manager.MarkControlReady()
	proofPath := filepath.Join(runtimeDir, npupdate.UpgradeProofFile)
	if _, err := os.Stat(proofPath); !os.IsNotExist(err) {
		t.Fatalf("proof existed before report: %v", err)
	}
	manager.MarkReportReady()
	body, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	var proof npupdate.UpgradeProof
	if err := json.Unmarshal(body, &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Version != "v1.2.4" || proof.AgentID != executorAgentID || proof.CommandID != commandID || proof.BootID == "" {
		t.Fatalf("proof=%+v", proof)
	}
}
