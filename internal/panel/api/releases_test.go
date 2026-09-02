package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
	"github.com/s2005lg/net-probe/internal/panel/auth"
	npupdate "github.com/s2005lg/net-probe/internal/update"
)

func TestImportReleaseFromGitHubTag(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest, signature := testSignedRelease(t, private, "v1.2.3", "linux", "amd64")
	oldBase := githubReleaseBase
	oldClient := githubHTTPClient
	githubReleaseBase = "https://github.test/releases/download"
	githubHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/releases/download/v1.2.3/net-probe_linux_amd64.manifest.json":
			raw, err := json.Marshal(manifest)
			if err != nil {
				return nil, err
			}
			body = string(raw)
		case "/releases/download/v1.2.3/net-probe_linux_amd64.manifest.sig":
			body = signature + "\n"
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { githubReleaseBase = oldBase })
	t.Cleanup(func() { githubHTTPClient = oldClient })

	d, cfg := openTestDB(t)
	server := New(d, cfg)
	server.PanelVersion = "v1.2.3"
	server.releaseKey = StaticReleasePublicKey(public)
	token := createAdminSession(t, d)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/releases/github", bytes.NewReader([]byte(`{"version":"v1.2.3","os":"linux","arch":"amd64"}`)))
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: token})
	rr := httptest.NewRecorder()
	server.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var storedVersion, storedArch string
	if err := d.QueryRow(`SELECT version,arch FROM agent_releases`).Scan(&storedVersion, &storedArch); err != nil {
		t.Fatal(err)
	}
	if storedVersion != "v1.2.3" || storedArch != "amd64" {
		t.Fatalf("stored version=%q arch=%q", storedVersion, storedArch)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func createAdminSession(t *testing.T, d *sql.DB) string {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO users(username,password_hash,created_at,role) VALUES('release-admin','x',?,'admin')`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := d.QueryRow(`SELECT id FROM users WHERE username='release-admin'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	token, err := auth.NewSession(d, userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func testSignedRelease(t *testing.T, private ed25519.PrivateKey, version, goos, arch string) (npupdate.Manifest, string) {
	t.Helper()
	artifact := []byte("agent")
	digest := sha256.Sum256(artifact)
	now := time.Now().UTC().Truncate(time.Second)
	manifest := npupdate.Manifest{
		Version: version, OS: goos, Arch: arch, ByteSize: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:]),
		ArtifactURL:         "https://github.com/s2005lg/net-probe/releases/download/" + version + "/net-probe_" + goos + "_" + arch,
		MinimumPanelVersion: version, ControlVersion: controlproto.Version, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	}
	signed, err := npupdate.SignManifest(private, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, base64.StdEncoding.EncodeToString(signed.Signature)
}
