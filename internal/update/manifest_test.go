package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func signedManifest(t *testing.T, version, goos, arch string, artifact []byte) (ed25519.PublicKey, SignedManifest) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	digest := sha256.Sum256(artifact)
	manifest := Manifest{
		Version: version, OS: goos, Arch: arch, ByteSize: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:]),
		ArtifactURL: "https://releases.example.com/" + version + "/net-probe", MinimumPanelVersion: "v1.2.0",
		ControlVersion: "1", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	}
	signed, err := SignManifest(private, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return public, signed
}

func validVerifyOptions(artifact []byte) VerifyOptions {
	return VerifyOptions{
		CurrentVersion: "v1.2.3", PanelVersion: "v1.2.3", OS: "linux", Arch: "amd64",
		ControlVersion: "1", Artifact: artifact, Now: time.Now().UTC(),
	}
}

func TestVerifyManifestAcceptsExactSignedArtifact(t *testing.T) {
	artifact := []byte("signed-agent-binary")
	public, signed := signedManifest(t, "v1.2.4", "linux", "amd64", artifact)
	if err := VerifyManifest(public, signed, validVerifyOptions(artifact)); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyManifestRejectsDowngradeAndPlatformMismatch(t *testing.T) {
	artifact := []byte("agent")
	public, signed := signedManifest(t, "v1.2.2", "linux", "amd64", artifact)
	if err := VerifyManifest(public, signed, validVerifyOptions(artifact)); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade err=%v", err)
	}
	public, signed = signedManifest(t, "v1.2.4", "linux", "arm64", artifact)
	if err := VerifyManifest(public, signed, validVerifyOptions(artifact)); !errors.Is(err, ErrPlatform) {
		t.Fatalf("platform err=%v", err)
	}
}

func TestVerifyManifestRejectsSignatureTimeAndCompatibilityFailures(t *testing.T) {
	artifact := []byte("agent")
	public, signed := signedManifest(t, "v1.2.4", "linux", "amd64", artifact)
	mutated := signed
	mutated.Manifest.ArtifactURL = "https://evil.example.com/net-probe"
	if err := VerifyManifest(public, mutated, validVerifyOptions(artifact)); !errors.Is(err, ErrSignature) {
		t.Fatalf("signature err=%v", err)
	}

	public, signed = signedManifest(t, "v1.2.4", "linux", "amd64", artifact)
	signed.Manifest.IssuedAt = time.Now().Add(6 * time.Minute).Unix()
	if err := resignWithFreshKey(t, &public, &signed); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(public, signed, validVerifyOptions(artifact)); !errors.Is(err, ErrIssuedInFuture) {
		t.Fatalf("future err=%v", err)
	}

	public, signed = signedManifest(t, "v1.2.4", "linux", "amd64", artifact)
	signed.Manifest.ExpiresAt = time.Now().Add(-time.Second).Unix()
	if err := resignWithFreshKey(t, &public, &signed); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(public, signed, validVerifyOptions(artifact)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry err=%v", err)
	}

	public, signed = signedManifest(t, "v1.2.4", "linux", "amd64", artifact)
	opts := validVerifyOptions(artifact)
	opts.ControlVersion = "2"
	if err := VerifyManifest(public, signed, opts); !errors.Is(err, ErrControlVersion) {
		t.Fatalf("control err=%v", err)
	}
	opts = validVerifyOptions(artifact)
	opts.PanelVersion = "v1.1.9"
	if err := VerifyManifest(public, signed, opts); !errors.Is(err, ErrPanelVersion) {
		t.Fatalf("Panel err=%v", err)
	}
}

func TestVerifyManifestRejectsArtifactAndURLViolations(t *testing.T) {
	artifact := []byte("agent")
	public, signed := signedManifest(t, "v1.2.4", "linux", "amd64", artifact)
	for name, test := range map[string]struct {
		mutate func(*Manifest)
		want   error
	}{
		"http":     {func(m *Manifest) { m.ArtifactURL = "http://releases.example.com/net-probe" }, ErrArtifactURL},
		"userinfo": {func(m *Manifest) { m.ArtifactURL = "https://token@releases.example.com/net-probe" }, ErrArtifactURL},
		"oversize": {func(m *Manifest) { m.ByteSize = MaxArtifactBytes + 1 }, ErrArtifactSize},
		"hash":     {func(m *Manifest) { m.SHA256 = "not-a-sha256" }, ErrArtifactHash},
	} {
		t.Run(name, func(t *testing.T) {
			changed := signed
			test.mutate(&changed.Manifest)
			if err := resignWithFreshKey(t, &public, &changed); err != nil {
				t.Fatal(err)
			}
			if err := VerifyManifest(public, changed, validVerifyOptions(artifact)); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
	public, signed = signedManifest(t, "v1.2.4", "linux", "amd64", artifact)
	if err := VerifyManifest(public, signed, validVerifyOptions([]byte("changed"))); !errors.Is(err, ErrArtifactSize) && !errors.Is(err, ErrArtifactHash) {
		t.Fatalf("changed artifact err=%v", err)
	}
}

func resignWithFreshKey(t *testing.T, public *ed25519.PublicKey, signed *SignedManifest) error {
	t.Helper()
	newPublic, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	updated, err := SignManifest(private, signed.Manifest)
	if err != nil {
		return err
	}
	*public, *signed = newPublic, updated
	return nil
}
