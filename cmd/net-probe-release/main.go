package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	npupdate "github.com/s2005lg/net-probe/internal/update"
)

const releaseKeyEnvironment = "NET_PROBE_RELEASE_SIGNING_KEY_B64"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("net-probe-release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	printPublic := flags.Bool("print-public-key", false, "print the release public key as lowercase hex")
	artifactPath := flags.String("artifact", "", "release artifact path")
	version := flags.String("version", "", "release semantic version")
	goos := flags.String("os", "", "target operating system")
	arch := flags.String("arch", "", "target architecture")
	artifactURL := flags.String("url", "", "final HTTPS artifact URL")
	minimumPanel := flags.String("minimum-panel", "", "minimum compatible Panel version")
	manifestPath := flags.String("manifest", "", "manifest output path")
	signaturePath := flags.String("signature", "", "detached signature output path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	private, err := releasePrivateKey(getenv(releaseKeyEnvironment))
	if err != nil {
		fmt.Fprintln(stderr, "release signing key is unavailable")
		return 2
	}
	public := private.Public().(ed25519.PublicKey)
	if *printPublic {
		if *artifactPath != "" || *version != "" || *goos != "" || *arch != "" || *artifactURL != "" || *minimumPanel != "" || *manifestPath != "" || *signaturePath != "" {
			fmt.Fprintln(stderr, "-print-public-key cannot be combined with signing flags")
			return 2
		}
		fmt.Fprintln(stdout, hex.EncodeToString(public))
		return 0
	}
	if *artifactPath == "" || *version == "" || *goos == "" || *arch == "" || *artifactURL == "" || *minimumPanel == "" || *manifestPath == "" || *signaturePath == "" {
		fmt.Fprintln(stderr, "all release manifest flags are required")
		return 2
	}
	artifact, err := readArtifact(*artifactPath)
	if err != nil {
		fmt.Fprintln(stderr, "read release artifact")
		return 1
	}
	digest := sha256.Sum256(artifact)
	now := time.Now().UTC().Truncate(time.Second)
	manifest := npupdate.Manifest{
		Version: *version, OS: *goos, Arch: *arch, ByteSize: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:]),
		ArtifactURL: *artifactURL, MinimumPanelVersion: *minimumPanel, ControlVersion: controlproto.Version,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(6 * time.Hour).Unix(),
	}
	signed, err := npupdate.SignManifest(private, manifest)
	if err != nil {
		fmt.Fprintln(stderr, "sign release manifest")
		return 1
	}
	if err := npupdate.VerifyManifest(public, signed, npupdate.VerifyOptions{
		CurrentVersion: "v0.0.0", PanelVersion: *minimumPanel, OS: *goos, Arch: *arch,
		ControlVersion: controlproto.Version, Artifact: artifact, Now: now,
	}); err != nil {
		fmt.Fprintln(stderr, "verify generated release manifest")
		return 1
	}
	manifestBody, err := npupdate.ManifestBytes(manifest)
	if err != nil {
		return 1
	}
	if err := writeAtomic(*manifestPath, append(manifestBody, '\n'), 0o644); err != nil {
		fmt.Fprintln(stderr, "write release manifest")
		return 1
	}
	signatureBody := []byte(base64.StdEncoding.EncodeToString(signed.Signature) + "\n")
	if err := writeAtomic(*signaturePath, signatureBody, 0o644); err != nil {
		fmt.Fprintln(stderr, "write release signature")
		return 1
	}
	fmt.Fprintln(stdout, "release manifest verified")
	return 0
}

func releasePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != ed25519.PrivateKeySize || base64.StdEncoding.EncodeToString(key) != encoded {
		return nil, fmt.Errorf("invalid release private key")
	}
	return ed25519.PrivateKey(key), nil
}

func readArtifact(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, npupdate.MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) == 0 || int64(len(body)) > npupdate.MaxArtifactBytes {
		return nil, npupdate.ErrArtifactSize
	}
	return body, nil
}

func writeAtomic(target string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	temporary, err := os.CreateTemp(dir, ".release-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
