package egressip

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"time"
)

const maxCacheBytes = 4 * 1024

type cacheEntry struct {
	Address    string    `json:"address,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

type cacheFile struct {
	IPv4 cacheEntry `json:"ipv4,omitempty"`
	IPv6 cacheEntry `json:"ipv6,omitempty"`
}

func readCache(path string) (cacheFile, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return cacheFile{}, nil
	}
	if err != nil {
		return cacheFile{}, fmt.Errorf("open egress IP cache: %w", err)
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxCacheBytes+1))
	if err != nil {
		return cacheFile{}, fmt.Errorf("read egress IP cache: %w", err)
	}
	if len(b) > maxCacheBytes {
		return cacheFile{}, fmt.Errorf("egress IP cache exceeds %d bytes", maxCacheBytes)
	}
	var cache cacheFile
	if err := json.Unmarshal(b, &cache); err != nil {
		return cacheFile{}, fmt.Errorf("decode egress IP cache: %w", err)
	}
	if err := validateCacheEntry(cache.IPv4, IPv4); err != nil {
		return cacheFile{}, err
	}
	if err := validateCacheEntry(cache.IPv6, IPv6); err != nil {
		return cacheFile{}, err
	}
	return cache, nil
}

func validateCacheEntry(entry cacheEntry, family Family) error {
	if entry.Address == "" {
		return nil
	}
	addr, err := netip.ParseAddr(entry.Address)
	if err != nil {
		return fmt.Errorf("egress IP cache contains an invalid %s address", familyName(family))
	}
	addr = addr.Unmap()
	if !matchesFamily(addr, family) || !isPublic(addr) {
		return fmt.Errorf("egress IP cache contains an invalid %s address", familyName(family))
	}
	return nil
}

func writeCache(path string, cache cacheFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create egress IP cache directory: %w", err)
	}
	f, err := os.CreateTemp(dir, ".egress-ip-cache-*")
	if err != nil {
		return fmt.Errorf("create temporary egress IP cache: %w", err)
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)

	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("set egress IP cache permissions: %w", err)
	}
	if err := json.NewEncoder(f).Encode(cache); err != nil {
		_ = f.Close()
		return fmt.Errorf("encode egress IP cache: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close egress IP cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace egress IP cache: %w", err)
	}
	return nil
}

func familyName(family Family) string {
	switch family {
	case IPv4:
		return "IPv4"
	case IPv6:
		return "IPv6"
	default:
		return "unknown-family"
	}
}
