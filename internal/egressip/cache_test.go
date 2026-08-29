package egressip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAndReadCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "egress-ip-cache.json")
	want := cacheFile{
		IPv4: cacheEntry{Address: "8.8.8.8", ObservedAt: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)},
	}
	if err := writeCache(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := readCache(path)
	if err != nil || got != want {
		t.Fatalf("readCache() = %+v, %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestReadCacheRejectsInvalidAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "egress-ip-cache.json")
	if err := os.WriteFile(path, []byte(`{"ipv4":{"address":"127.0.0.1","observed_at":"2026-08-28T01:02:03Z"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCache(path); err == nil {
		t.Fatal("expected invalid cache error")
	}
}

func TestReadCacheRejectsCorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "egress-ip-cache.json")
	if err := os.WriteFile(path, []byte(`{"ipv4":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCache(path); err == nil {
		t.Fatal("expected corrupt cache error")
	}
}

func TestReadCacheRejectsInputLargerThanFourKiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "egress-ip-cache.json")
	oversized := `{"extra":"` + strings.Repeat("x", maxCacheBytes) + `"}`
	if err := os.WriteFile(path, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCache(path); err == nil {
		t.Fatal("expected oversized cache error")
	}
}

func TestReadCacheMissingReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	got, err := readCache(path)
	if err != nil || got != (cacheFile{}) {
		t.Fatalf("readCache() = %+v, %v", got, err)
	}
}

func TestReadCacheKeepsLegacySuccessOnlyShapeCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-cache.json")
	if err := os.WriteFile(path, []byte(`{"ipv4":{"address":"8.8.8.8","observed_at":"2026-08-28T01:02:03Z"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readCache(path)
	if err != nil {
		t.Fatal(err)
	}
	wantTime := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	if got.IPv4.Address != "8.8.8.8" || !got.IPv4.ObservedAt.Equal(wantTime) ||
		got.IPv4.LastAttemptAt != 0 || got.IPv4.FailureCount != 0 || got.IPv4.RetryAt != 0 {
		t.Fatalf("legacy cache = %+v", got)
	}
}

func TestWriteCacheAtomicallyReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "egress-ip-cache.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := cacheFile{
		IPv6: cacheEntry{Address: "2001:4860:4860::8888", ObservedAt: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)},
	}
	if err := writeCache(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := readCache(path)
	if err != nil || got != want {
		t.Fatalf("readCache() = %+v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("directory entries = %v", entries)
	}
}
