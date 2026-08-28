package egressip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolverUsesFreshCacheWithoutNetwork(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := writeCache(path, cacheFile{IPv4: cacheEntry{Address: "8.8.8.8", ObservedAt: now.Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r := Resolver{
		Now: func() time.Time { return now },
		Discover: func(context.Context, Family, []string) (string, error) {
			calls++
			return "", errors.New("must not be called")
		},
	}
	got := r.Resolve(context.Background(), Options{Enabled: true, RefreshInterval: 6 * time.Hour, CachePath: path})
	if got.IPv4 != "8.8.8.8" || calls != 0 {
		t.Fatalf("result=%+v calls=%d", got, calls)
	}
}

func TestResolverRetriesStaleCacheAndKeepsItOnFailure(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := writeCache(path, cacheFile{IPv4: cacheEntry{Address: "8.8.8.8", ObservedAt: now.Add(-7 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r := Resolver{
		Now: func() time.Time { return now },
		Discover: func(_ context.Context, family Family, _ []string) (string, error) {
			calls++
			if family != IPv4 {
				t.Fatalf("family = %v", family)
			}
			return "", errors.New("offline")
		},
	}
	got := r.Resolve(context.Background(), Options{Enabled: true, RefreshInterval: 6 * time.Hour, CachePath: path})
	if got.IPv4 != "8.8.8.8" || calls != 1 {
		t.Fatalf("result=%+v calls=%d", got, calls)
	}
	cache, err := readCache(path)
	want := cacheEntry{Address: "8.8.8.8", ObservedAt: now.Add(-7 * time.Hour)}
	if err != nil || cache.IPv4 != want {
		t.Fatalf("cache = %+v, err = %v", cache, err)
	}
}

func TestResolverDiscoversBothFamiliesOnFirstRun(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "cache.json")
	r := Resolver{
		Now: func() time.Time { return now },
		Discover: func(_ context.Context, family Family, _ []string) (string, error) {
			switch family {
			case IPv4:
				return "8.8.4.4", nil
			case IPv6:
				return "2001:4860:4860::8844", nil
			default:
				return "", errors.New("unexpected family")
			}
		},
	}
	got := r.Resolve(context.Background(), Options{
		Enabled: true, RefreshInterval: 6 * time.Hour, Timeout: time.Second, CachePath: path,
		IPv4Endpoints: []string{"https://v4.test/ip"}, IPv6Endpoints: []string{"https://v6.test/ip"},
	})
	if got != (Result{IPv4: "8.8.4.4", IPv6: "2001:4860:4860::8844"}) {
		t.Fatalf("result = %+v", got)
	}
	cache, err := readCache(path)
	if err != nil || cache.IPv4.ObservedAt != now || cache.IPv6.ObservedAt != now {
		t.Fatalf("cache = %+v, err = %v", cache, err)
	}
}

func TestResolverKeepsFamilyFailuresIndependent(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "cache.json")
	stale := now.Add(-7 * time.Hour)
	if err := writeCache(path, cacheFile{
		IPv4: cacheEntry{Address: "8.8.8.8", ObservedAt: stale},
		IPv6: cacheEntry{Address: "2001:4860:4860::8888", ObservedAt: stale},
	}); err != nil {
		t.Fatal(err)
	}
	r := Resolver{
		Now: func() time.Time { return now },
		Discover: func(_ context.Context, family Family, _ []string) (string, error) {
			if family == IPv4 {
				return "", errors.New("IPv4 offline")
			}
			return "2606:4700:4700::1111", nil
		},
	}
	got := r.Resolve(context.Background(), Options{
		Enabled: true, RefreshInterval: 6 * time.Hour, Timeout: time.Second, CachePath: path,
		IPv4Endpoints: []string{"https://v4.test/ip"}, IPv6Endpoints: []string{"https://v6.test/ip"},
	})
	if got != (Result{IPv4: "8.8.8.8", IPv6: "2606:4700:4700::1111"}) {
		t.Fatalf("result = %+v", got)
	}
}

func TestResolverDisabledDoesNotReadCacheOrDiscover(t *testing.T) {
	r := Resolver{Discover: func(context.Context, Family, []string) (string, error) {
		t.Fatal("Discover called while disabled")
		return "", nil
	}}
	got := r.Resolve(context.Background(), Options{CachePath: filepath.Join(t.TempDir(), "missing", "cache.json")})
	if got != (Result{}) {
		t.Fatalf("result = %+v", got)
	}
}

func TestResolverRecoversFromCorruptCache(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Resolver{
		Now: func() time.Time { return now },
		Discover: func(context.Context, Family, []string) (string, error) {
			return "8.8.8.8", nil
		},
	}
	got := r.Resolve(context.Background(), Options{
		Enabled: true, RefreshInterval: 6 * time.Hour, Timeout: time.Second, CachePath: path,
		IPv4Endpoints: []string{"https://v4.test/ip"},
	})
	if got.IPv4 != "8.8.8.8" {
		t.Fatalf("result = %+v", got)
	}
	cache, err := readCache(path)
	if err != nil || cache.IPv4 != (cacheEntry{Address: "8.8.8.8", ObservedAt: now}) {
		t.Fatalf("cache = %+v, err = %v", cache, err)
	}
}

func TestResolverReplacesSuccessfulStaleEntry(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := writeCache(path, cacheFile{IPv4: cacheEntry{Address: "8.8.8.8", ObservedAt: now.Add(-7 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	r := Resolver{
		Now: func() time.Time { return now },
		Discover: func(context.Context, Family, []string) (string, error) {
			return "1.1.1.1", nil
		},
	}
	got := r.Resolve(context.Background(), Options{Enabled: true, RefreshInterval: 6 * time.Hour, Timeout: time.Second, CachePath: path})
	if got.IPv4 != "1.1.1.1" {
		t.Fatalf("result = %+v", got)
	}
	cache, err := readCache(path)
	if err != nil || cache.IPv4 != (cacheEntry{Address: "1.1.1.1", ObservedAt: now}) {
		t.Fatalf("cache = %+v, err = %v", cache, err)
	}
}

func TestResolverReturnsFreshResultWhenCacheWriteFails(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Resolver{Discover: func(context.Context, Family, []string) (string, error) {
		return "8.8.8.8", nil
	}}
	got := r.Resolve(context.Background(), Options{
		Enabled: true, RefreshInterval: time.Hour, Timeout: time.Second,
		CachePath: filepath.Join(blocker, "cache.json"), IPv4Endpoints: []string{"https://v4.test/ip"},
	})
	if got.IPv4 != "8.8.8.8" {
		t.Fatalf("result = %+v", got)
	}
}

func TestResolverRefreshesFamiliesConcurrentlyWithSeparateTimeouts(t *testing.T) {
	started := make(chan Family, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	var contexts []context.Context
	r := Resolver{Discover: func(ctx context.Context, family Family, endpoints []string) (string, error) {
		if len(endpoints) != 2 {
			return "", errors.New("family did not receive all fallback endpoints")
		}
		if _, ok := ctx.Deadline(); !ok {
			return "", errors.New("family has no timeout")
		}
		mu.Lock()
		contexts = append(contexts, ctx)
		mu.Unlock()
		started <- family
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if family == IPv4 {
			return "8.8.8.8", nil
		}
		return "2001:4860:4860::8888", nil
	}}

	cachePath := filepath.Join(t.TempDir(), "cache.json")
	done := make(chan Result, 1)
	go func() {
		done <- r.Resolve(context.Background(), Options{
			Enabled: true, RefreshInterval: time.Hour, Timeout: time.Second,
			CachePath:     cachePath,
			IPv4Endpoints: []string{"https://v4-a.test", "https://v4-b.test"},
			IPv6Endpoints: []string{"https://v6-a.test", "https://v6-b.test"},
		})
	}()

	seen := map[Family]bool{}
	for range 2 {
		select {
		case family := <-started:
			seen[family] = true
		case <-time.After(500 * time.Millisecond):
			t.Fatal("families did not start concurrently")
		}
	}
	close(release)
	got := <-done
	if !seen[IPv4] || !seen[IPv6] || got.IPv4 == "" || got.IPv6 == "" {
		t.Fatalf("seen=%v result=%+v", seen, got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(contexts) != 2 || contexts[0] == contexts[1] {
		t.Fatalf("contexts = %v", contexts)
	}
}

func TestResolverLogsOnlySafeFamilyAndCategory(t *testing.T) {
	const raw = "raw-provider-error at https://secret-provider.test/body"
	var logs []string
	r := Resolver{Discover: func(context.Context, Family, []string) (string, error) {
		return "", errors.New(raw)
	}}
	r.Resolve(context.Background(), Options{
		Enabled: true, RefreshInterval: time.Hour, Timeout: time.Second,
		CachePath:     filepath.Join(t.TempDir(), "cache.json"),
		IPv4Endpoints: []string{"https://secret-provider.test/body"},
		Logf: func(format string, args ...any) {
			logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
		},
	})
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "family=IPv4") || !strings.Contains(joined, "category=unavailable") {
		t.Fatalf("logs = %q", logs)
	}
	for _, secret := range []string{raw, "secret-provider.test", "https://"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("logs exposed %q: %q", secret, logs)
		}
	}
}
