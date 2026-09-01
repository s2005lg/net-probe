package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	npupdate "github.com/s2005lg/net-probe/internal/update"
)

func TestRunReleaseSignerRequiresKeyAndReverifiesOutputs(t *testing.T) {
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "net-probe")
	manifestPath := filepath.Join(dir, "net-probe.manifest.json")
	signaturePath := filepath.Join(dir, "net-probe.manifest.sig")
	artifact := []byte("release-agent-binary")
	if err := os.WriteFile(artifactPath, artifact, 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"-artifact", artifactPath, "-version", "v1.2.4", "-os", runtime.GOOS, "-arch", runtime.GOARCH,
		"-url", "https://releases.example.com/v1.2.4/net-probe", "-minimum-panel", "v1.2.0",
		"-manifest", manifestPath, "-signature", signaturePath,
	}
	if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" }); code == 0 {
		t.Fatal("signing succeeded without release key")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(private)
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, func(name string) string {
		if name == releaseKeyEnvironment {
			return key
		}
		return ""
	}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	manifestBody, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := npupdate.DecodeManifest(bytes.TrimSpace(manifestBody))
	if err != nil {
		t.Fatal(err)
	}
	signatureBody, err := os.ReadFile(signaturePath)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signatureBody)))
	if err != nil {
		t.Fatal(err)
	}
	if err := npupdate.VerifyManifest(private.Public().(ed25519.PublicKey), npupdate.SignedManifest{Manifest: manifest, Signature: signature}, npupdate.VerifyOptions{
		CurrentVersion: "v1.2.3", PanelVersion: "v1.2.3", OS: runtime.GOOS, Arch: runtime.GOARCH,
		ControlVersion: "1", Artifact: artifact,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunReleaseSignerPrintsDerivedPublicKey(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-print-public-key"}, &stdout, &stderr, func(string) string {
		return base64.StdEncoding.EncodeToString(private)
	})
	if code != 0 || strings.TrimSpace(stdout.String()) != hex.EncodeToString(public) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
