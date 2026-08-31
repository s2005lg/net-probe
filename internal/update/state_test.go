package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func validUpdateRequest(t *testing.T, dir string) (UpdateRequest, RequestValidationOptions) {
	t.Helper()
	artifact := []byte("verified-update-binary")
	basename := "net-probe-v1.2.4"
	path := filepath.Join(dir, basename)
	if err := os.WriteFile(path, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	now := time.Now().UTC().Truncate(time.Second)
	signed, err := SignManifest(private, Manifest{
		Version: "v1.2.4", OS: runtime.GOOS, Arch: runtime.GOARCH, ByteSize: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:]),
		ArtifactURL: "https://releases.example.com/v1.2.4/net-probe", MinimumPanelVersion: "v1.2.0",
		ControlVersion: "1", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := UpdateRequest{
		ArtifactBasename: basename, Manifest: signed.Manifest, Signature: base64.StdEncoding.EncodeToString(signed.Signature),
		PreviousVersion: "v1.2.3", AgentID: "123e4567-e89b-42d3-a456-426614174040", CommandID: "123e4567-e89b-42d3-a456-426614174041",
	}
	return request, RequestValidationOptions{
		UpdatesDir: dir, PublicKey: public, CurrentVersion: "v1.2.3", PanelVersion: "v1.2.3",
		OS: runtime.GOOS, Arch: runtime.GOARCH, ControlVersion: "1", AgentID: request.AgentID,
		ExpectedUID: os.Getuid(), ExpectedGID: os.Getgid(),
	}
}

func TestValidateUpdateRequestAcceptsStrictOwnedRegularArtifact(t *testing.T) {
	dir := t.TempDir()
	request, options := validUpdateRequest(t, dir)
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	validated, artifact, err := ValidateUpdateRequest(body, options)
	if err != nil {
		t.Fatal(err)
	}
	if validated.CommandID != request.CommandID || string(artifact) != "verified-update-binary" {
		t.Fatalf("validated=%+v artifact=%q", validated, artifact)
	}
}

func TestValidateUpdateRequestRejectsUnknownFieldsTraversalAndSymlink(t *testing.T) {
	dir := t.TempDir()
	request, options := validUpdateRequest(t, dir)
	body, _ := json.Marshal(request)
	body = append(body[:len(body)-1], []byte(`,"unknown":true}`)...)
	if _, _, err := ValidateUpdateRequest(body, options); err == nil {
		t.Fatal("accepted unknown request field")
	}

	for _, name := range []string{"../net-probe", "/tmp/net-probe", `..\net-probe`, ".", ""} {
		changed := request
		changed.ArtifactBasename = name
		body, _ = json.Marshal(changed)
		if _, _, err := ValidateUpdateRequest(body, options); err == nil {
			t.Fatalf("accepted basename %q", name)
		}
	}

	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("verified-update-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked-artifact")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	request.ArtifactBasename = filepath.Base(link)
	body, _ = json.Marshal(request)
	if _, _, err := ValidateUpdateRequest(body, options); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("symlink err=%v", err)
	}
}

func TestValidateUpdateRequestRejectsWrongModeOwnerAndChangedArtifact(t *testing.T) {
	dir := t.TempDir()
	request, options := validUpdateRequest(t, dir)
	path := filepath.Join(dir, request.ArtifactBasename)
	body, _ := json.Marshal(request)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ValidateUpdateRequest(body, options); err == nil {
		t.Fatal("accepted group/world-readable artifact")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	options.ExpectedUID++
	if _, _, err := ValidateUpdateRequest(body, options); err == nil {
		t.Fatal("accepted wrong artifact owner")
	}
	options.ExpectedUID--
	if err := os.WriteFile(path, []byte("changed-update-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ValidateUpdateRequest(body, options); err == nil {
		t.Fatal("accepted changed artifact")
	}
}
