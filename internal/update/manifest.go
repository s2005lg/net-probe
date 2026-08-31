package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const MaxArtifactBytes int64 = 32 << 20

var (
	ErrSignature      = errors.New("invalid release signature")
	ErrDowngrade      = errors.New("release is not newer than current version")
	ErrPlatform       = errors.New("release platform does not match")
	ErrArtifactSize   = errors.New("release artifact size is invalid")
	ErrArtifactHash   = errors.New("release artifact hash is invalid")
	ErrArtifactURL    = errors.New("release artifact URL is invalid")
	ErrExpired        = errors.New("release manifest is expired")
	ErrIssuedInFuture = errors.New("release manifest was issued in the future")
	ErrPanelVersion   = errors.New("Panel version is below release minimum")
	ErrControlVersion = errors.New("release control version does not match")
	ErrVersion        = errors.New("release version is invalid")
)

// Manifest has a deliberately closed schema. Its canonical JSON encoding is
// the exact byte sequence signed by release automation.
type Manifest struct {
	Version             string `json:"version"`
	OS                  string `json:"os"`
	Arch                string `json:"arch"`
	ByteSize            int64  `json:"byte_size"`
	SHA256              string `json:"sha256"`
	ArtifactURL         string `json:"artifact_url"`
	MinimumPanelVersion string `json:"minimum_panel_version"`
	ControlVersion      string `json:"control_version"`
	IssuedAt            int64  `json:"issued_at"`
	ExpiresAt           int64  `json:"expires_at"`
}

type SignedManifest struct {
	Manifest  Manifest
	Signature []byte
}

type VerifyOptions struct {
	CurrentVersion string
	PanelVersion   string
	OS             string
	Arch           string
	ControlVersion string
	Artifact       []byte
	Now            time.Time
}

func SignManifest(privateKey ed25519.PrivateKey, manifest Manifest) (SignedManifest, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return SignedManifest{}, ErrSignature
	}
	body, err := ManifestBytes(manifest)
	if err != nil {
		return SignedManifest{}, err
	}
	return SignedManifest{Manifest: manifest, Signature: ed25519.Sign(privateKey, body)}, nil
}

func VerifyManifest(publicKey ed25519.PublicKey, signed SignedManifest, options VerifyOptions) error {
	if len(publicKey) != ed25519.PublicKeySize || len(signed.Signature) != ed25519.SignatureSize {
		return ErrSignature
	}
	body, err := ManifestBytes(signed.Manifest)
	if err != nil || !ed25519.Verify(publicKey, body, signed.Signature) {
		return ErrSignature
	}
	manifest := signed.Manifest
	version, ok := parseSemver(manifest.Version)
	if !ok {
		return ErrVersion
	}
	current, ok := parseSemver(options.CurrentVersion)
	if !ok || compareVersion(version, current) <= 0 {
		return ErrDowngrade
	}
	if manifest.OS == "" || manifest.Arch == "" || manifest.OS != options.OS || manifest.Arch != options.Arch {
		return ErrPlatform
	}
	if manifest.ByteSize <= 0 || manifest.ByteSize > MaxArtifactBytes {
		return ErrArtifactSize
	}
	digest, err := hex.DecodeString(manifest.SHA256)
	if err != nil || len(digest) != sha256.Size || manifest.SHA256 != strings.ToLower(manifest.SHA256) {
		return ErrArtifactHash
	}
	artifactURL, err := url.Parse(manifest.ArtifactURL)
	if err != nil || artifactURL.Scheme != "https" || artifactURL.Hostname() == "" || artifactURL.User != nil || artifactURL.RawQuery != "" || artifactURL.Fragment != "" {
		return ErrArtifactURL
	}
	if options.ControlVersion == "" || manifest.ControlVersion != options.ControlVersion {
		return ErrControlVersion
	}
	minimumPanel, ok := parseSemver(manifest.MinimumPanelVersion)
	if !ok {
		return ErrPanelVersion
	}
	panel, ok := parseSemver(options.PanelVersion)
	if !ok || compareVersion(panel, minimumPanel) < 0 {
		return ErrPanelVersion
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	nowUnix := now.Unix()
	if manifest.ExpiresAt <= nowUnix || manifest.ExpiresAt <= manifest.IssuedAt {
		return ErrExpired
	}
	if manifest.IssuedAt > now.Add(5*time.Minute).Unix() {
		return ErrIssuedInFuture
	}
	if manifest.ExpiresAt-manifest.IssuedAt > int64(24*time.Hour/time.Second) {
		return ErrExpired
	}
	if options.Artifact != nil {
		if int64(len(options.Artifact)) != manifest.ByteSize {
			return ErrArtifactSize
		}
		actual := sha256.Sum256(options.Artifact)
		if !bytes.Equal(actual[:], digest) {
			return ErrArtifactHash
		}
	}
	return nil
}

func ManifestBytes(manifest Manifest) ([]byte, error) {
	return json.Marshal(manifest)
}

func DecodeManifest(body []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("release manifest contains trailing JSON")
	}
	canonical, err := ManifestBytes(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.Equal(bytes.TrimSpace(body), canonical) {
		return Manifest{}, errors.New("release manifest is not canonical JSON")
	}
	return manifest, nil
}

type semanticVersion [3]uint64

func parseSemver(value string) (semanticVersion, bool) {
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return semanticVersion{}, false
	}
	var out semanticVersion
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semanticVersion{}, false
		}
		number, err := strconv.ParseUint(part, 10, 63)
		if err != nil {
			return semanticVersion{}, false
		}
		out[index] = number
	}
	return out, true
}

func compareVersion(left, right semanticVersion) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}
