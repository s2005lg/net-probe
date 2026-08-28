# Agent Egress IP and Panel Geolocation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every Agent discover and report its public IPv4/IPv6 addresses, then have the Panel immediately resolve and display the address's country/region without waiting for the periodic geolocation scan.

**Architecture:** The oneshot Agent discovers public addresses through family-specific HTTPS endpoints and reuses a validated on-disk cache between one-minute runs. The Panel treats the reported address as untrusted input, records which IP its stored geography belongs to, and sends new or stale addresses through one deduplicating asynchronous worker backed by a persistent SQLite cache. The frontend reads the additive Host fields and labels the IP and geography separately.

**Tech Stack:** Go standard library (`net/http`, `net/netip`, `database/sql`), BurntSushi TOML, modernc SQLite, React 18, TypeScript, Vitest, Vite.

## Global Constraints

- Preserve report `schema_version = "1"`; `egress_ipv4` and `egress_ipv6` are optional additive fields.
- Never overwrite `host.ipv4` or `host.ipv6`; those remain local interface diagnostics.
- Agent discovery is enabled by default, refreshes every `6h`, and uses a `3s` timeout per family.
- Default IPv4 endpoints are `https://api.ipify.org` then `https://4.ident.me`.
- Default IPv6 endpoints are `https://api6.ipify.org` then `https://6.ident.me`.
- Default Panel geolocation is `https://ipwho.is/{ip}?lang=zh-CN` with a `4s` timeout and `12h` freshness.
- External discovery and geolocation failures never fail Agent report delivery or mark a node/service unhealthy.
- All default external calls use HTTPS; redirects may not downgrade to HTTP.
- Provider responses are size-limited and never copied into reports or logs.
- Old Agents and old Panels remain compatible in either deployment order.
- Keep the Agent binary dependency-free beyond its existing modules; use the Go standard library only.

---

## File map

### New files

- `internal/egressip/http.go` — family-specific HTTPS discovery and response validation.
- `internal/egressip/http_test.go` — discovery, fallback, response, and redirect tests.
- `internal/egressip/cache.go` — validated cache model plus atomic file reads/writes.
- `internal/egressip/cache_test.go` — cache corruption, permissions, and replacement tests.
- `internal/egressip/resolver.go` — refresh policy that combines discovery with cached results.
- `internal/egressip/resolver_test.go` — first-run, fresh, stale, partial-family, and failure behavior.
- `internal/panel/geo/address.go` — effective-address selection and public-address classification.
- `internal/panel/geo/address_test.go` — precedence and reserved-range tests.
- `internal/panel/geo/refresher.go` — deduplicating lookup worker, persistent cache, observation, and reconciliation.
- `internal/panel/geo/refresher_test.go` — immediate lookup, cache reuse, IP race, private IP, and reconciliation tests.
- `web/src/lib/host.ts` — one frontend address-selection rule.
- `web/src/lib/host.test.ts` — new/old Agent selection tests.

### Modified files

- `internal/report/types.go`, `internal/report/types_test.go` — additive Host contract.
- `internal/config/config.go`, `internal/config/config_test.go` — Agent discovery defaults and validation.
- `internal/agent/run.go`, `internal/agent/run_test.go` — resolve egress IP during report construction with test injection.
- `internal/panel/config/config.go`, `internal/panel/config/config_test.go` — secure provider settings.
- `internal/panel/db/db.go`, `internal/panel/db/db_test.go` — `ip_geo_ip` migration and `ip_geo_cache` table.
- `internal/panel/geo/geo.go`, `internal/panel/geo/geo_test.go` — HTTPS provider interface and IPWhois adapter.
- `internal/panel/api/api.go`, `internal/panel/api/report.go`, `internal/panel/api/report_test.go` — non-blocking observation after report persistence.
- `cmd/net-probe-panel/main.go` — construct/start the worker and retain periodic reconciliation.
- `web/src/lib/api.ts`, `web/src/pages/Nodes.tsx`, `web/src/pages/NodeDetail.tsx` — types, labels, and display values.
- `README.md` — English and Chinese configuration/behavior documentation.

---

### Task 1: Add the report contract and secure Agent configuration

**Files:**
- Modify: `internal/report/types.go`
- Modify: `internal/report/types_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces: `report.Host.EgressIPv4 string` and `report.Host.EgressIPv6 string`.
- Produces: `config.EgressIPConfig` at `Config.Collect.EgressIP`.
- Produces: validated duration strings and ordered endpoint lists consumed by Task 3.

- [ ] **Step 1: Write failing report serialization tests**

Append tests that prove populated fields serialize and empty fields remain omitted:

```go
func TestHostMarshalEgressIPs(t *testing.T) {
	b, err := json.Marshal(Host{EgressIPv4: "203.0.113.7", EgressIPv6: "2001:db8::7"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `"egress_ipv4":"203.0.113.7"`) ||
		!strings.Contains(got, `"egress_ipv6":"2001:db8::7"`) {
		t.Fatalf("host JSON = %s", got)
	}
}

func TestHostMarshalOmitsEmptyEgressIPs(t *testing.T) {
	b, err := json.Marshal(Host{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "egress_") {
		t.Fatalf("host JSON = %s", b)
	}
}
```

- [ ] **Step 2: Run the report tests and confirm the missing fields fail**

Run: `go test ./internal/report -run 'TestHostMarshal(EgressIPs|OmitsEmptyEgressIPs)$'`

Expected: `TestHostMarshalEgressIPs` fails because neither JSON key exists.

- [ ] **Step 3: Add the optional Host fields**

Add next to the existing interface addresses:

```go
IPv4       string `json:"ipv4,omitempty"`
IPv6       string `json:"ipv6,omitempty"`
EgressIPv4 string `json:"egress_ipv4,omitempty"`
EgressIPv6 string `json:"egress_ipv6,omitempty"`
```

- [ ] **Step 4: Write failing Agent configuration tests**

Add tests for defaults and unsafe endpoint rejection:

```go
func TestEgressIPDefaults(t *testing.T) {
	cfg := Default()
	got := cfg.Collect.EgressIP
	if !got.Enabled || got.RefreshInterval != "6h" || got.Timeout != "3s" {
		t.Fatalf("egress defaults = %+v", got)
	}
	if !reflect.DeepEqual(got.IPv4Endpoints, []string{"https://api.ipify.org", "https://4.ident.me"}) {
		t.Fatalf("IPv4 endpoints = %v", got.IPv4Endpoints)
	}
	if !reflect.DeepEqual(got.IPv6Endpoints, []string{"https://api6.ipify.org", "https://6.ident.me"}) {
		t.Fatalf("IPv6 endpoints = %v", got.IPv6Endpoints)
	}
}

func TestValidateRejectsUnsafeEgressEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Sinks = []Sink{{Type: "webhook", URL: "https://example.com/report"}}
	cfg.Collect.EgressIP.IPv4Endpoints = []string{"http://public.example/ip"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "egress IPv4 endpoint") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsLocalEgressEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Sinks = []Sink{{Type: "webhook", URL: "https://example.com/report"}}
	cfg.Collect.EgressIP.IPv4Endpoints = []string{"http://127.0.0.1:8080/ip"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 5: Run the configuration tests and confirm they fail**

Run: `go test ./internal/config -run 'Test(EgressIPDefaults|ValidateRejectsUnsafeEgressEndpoint|ValidateAcceptsLocalEgressEndpoint)$'`

Expected: compile failure because `Collect.EgressIP` does not exist.

- [ ] **Step 6: Implement configuration defaults and validation**

Add:

```go
type EgressIPConfig struct {
	Enabled         bool     `toml:"enabled"`
	RefreshInterval string   `toml:"refresh_interval"`
	Timeout         string   `toml:"timeout"`
	IPv4Endpoints   []string `toml:"ipv4_endpoints"`
	IPv6Endpoints   []string `toml:"ipv6_endpoints"`
}

type CollectConfig struct {
	DiskMounts []string       `toml:"disk_mounts"`
	Upgradable bool           `toml:"upgradable"`
	EgressIP   EgressIPConfig `toml:"egress_ip"`
}
```

Initialize all Global Constraints defaults in `Default()`. Extend `Validate()` to parse both durations as positive values and validate every endpoint with `net/url`: allow `https`, plus `http` only when `Hostname()` is `localhost`, `127.0.0.1`, or `::1`. Reject empty endpoint lists when discovery is enabled.

- [ ] **Step 7: Run focused and package tests**

Run: `go test ./internal/report ./internal/config`

Expected: PASS.

- [ ] **Step 8: Commit the contract and configuration**

```bash
git add internal/report/types.go internal/report/types_test.go internal/config/config.go internal/config/config_test.go
git commit -m "feat: define egress IP report contract"
```

---

### Task 2: Implement bounded HTTPS public-IP discovery

**Files:**
- Create: `internal/egressip/http.go`
- Create: `internal/egressip/http_test.go`

**Interfaces:**
- Consumes: ordered endpoints already validated by `config.Config.Validate()`.
- Produces: `egressip.Family`, `egressip.Fetcher`, `egressip.NewHTTPClient`, and `Fetcher.Discover` for Task 3.

- [ ] **Step 1: Write failing discovery tests**

Define table and fallback tests around this interface:

```go
func TestDiscoverValidatesFamily(t *testing.T) {
	tests := []struct {
		name   string
		family Family
		body   string
		want   string
		ok     bool
	}{
		{"ipv4", IPv4, " 8.8.8.8\n", "8.8.8.8", true},
		{"ipv6", IPv6, "2001:4860:4860::8888\n", "2001:4860:4860::8888", true},
		{"wrong family", IPv4, "2001:4860:4860::8888", "", false},
		{"private", IPv4, "192.168.1.2", "", false},
		{"loopback", IPv6, "::1", "", false},
		{"malformed", IPv4, "not-an-ip", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, tt.body), nil
			})}
			got, err := (Fetcher{Client: client}).Discover(context.Background(), tt.family, []string{"https://provider.test/ip"})
			if (err == nil) != tt.ok || got != tt.want {
				t.Fatalf("Discover() = %q, %v", got, err)
			}
		})
	}
}

func TestDiscoverFallsBackInOrder(t *testing.T) {
	var hosts []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Host)
		if req.URL.Host == "first.test" {
			return response(http.StatusServiceUnavailable, "unavailable"), nil
		}
		return response(http.StatusOK, "8.8.4.4"), nil
	})}
	got, err := (Fetcher{Client: client}).Discover(context.Background(), IPv4, []string{
		"https://first.test/ip", "https://second.test/ip",
	})
	if err != nil || got != "8.8.4.4" || !reflect.DeepEqual(hosts, []string{"first.test", "second.test"}) {
		t.Fatalf("got=%q err=%v hosts=%v", got, err, hosts)
	}
}
```

Also add one test returning more than 64 bytes and one redirect test proving `https://provider.test` cannot redirect to `http://provider.test`.

The test file defines its transport helpers explicitly:

```go
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
```

- [ ] **Step 2: Run tests and confirm the package is missing**

Run: `go test ./internal/egressip -run 'TestDiscover'`

Expected: compile failure because the package implementation does not exist.

- [ ] **Step 3: Implement the discovery API**

Create these exact exported definitions:

```go
type Family uint8

const (
	IPv4 Family = 4
	IPv6 Family = 6
)

type Fetcher struct {
	Client *http.Client
}

func NewHTTPClient(timeout time.Duration) *http.Client
func (f Fetcher) Discover(ctx context.Context, family Family, endpoints []string) (string, error)
```

`Discover` performs an HTTP GET, requires a 2xx response, reads at most 65 bytes, rejects a body longer than 64 bytes, trims whitespace, parses with `netip.ParseAddr`, calls `Unmap()`, checks the requested family, and rejects addresses for which any of these are true: `!IsGlobalUnicast()`, `IsPrivate()`, `IsLoopback()`, `IsLinkLocalUnicast()`, `IsMulticast()`, or `IsUnspecified()`.

`NewHTTPClient` sets the supplied timeout and a `CheckRedirect` function that rejects more than three redirects and any HTTPS-to-HTTP downgrade.

- [ ] **Step 4: Run discovery tests**

Run: `go test ./internal/egressip -run 'TestDiscover|TestHTTPClient'`

Expected: PASS.

- [ ] **Step 5: Run static analysis for the new package**

Run: `go vet ./internal/egressip`

Expected: no output and exit 0.

- [ ] **Step 6: Commit discovery**

```bash
git add internal/egressip/http.go internal/egressip/http_test.go
git commit -m "feat: discover public egress addresses"
```

---

### Task 3: Add persistent Agent caching and report integration

**Files:**
- Create: `internal/egressip/cache.go`
- Create: `internal/egressip/cache_test.go`
- Create: `internal/egressip/resolver.go`
- Create: `internal/egressip/resolver_test.go`
- Modify: `internal/agent/run.go`
- Modify: `internal/agent/run_test.go`

**Interfaces:**
- Consumes: `egressip.Fetcher.Discover` from Task 2 and `config.EgressIPConfig` from Task 1.
- Produces: `egressip.Options`, `egressip.Result`, `egressip.Resolver.Resolve`.
- Produces: reports populated with `host.egress_ipv4` and `host.egress_ipv6`.

- [ ] **Step 1: Write failing cache tests**

Use this internal model and assert round-trip validation:

```go
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
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v", info.Mode().Perm(), err)
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
```

Add a corrupt JSON test and a test that a pre-existing file is atomically replaced without leaving a temporary file in the directory.

- [ ] **Step 2: Run cache tests and confirm they fail**

Run: `go test ./internal/egressip -run 'Test(WriteAndReadCache|ReadCache|WriteCache)'`

Expected: compile failure for missing cache types/functions.

- [ ] **Step 3: Implement validated atomic cache IO**

Use these JSON types:

```go
type cacheEntry struct {
	Address    string    `json:"address,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

type cacheFile struct {
	IPv4 cacheEntry `json:"ipv4,omitempty"`
	IPv6 cacheEntry `json:"ipv6,omitempty"`
}
```

`readCache` accepts a missing file as an empty cache, limits JSON input to 4 KiB, and validates each non-empty entry with the same public/family rules as discovery. `writeCache` creates the parent directory with `0755`, writes JSON to `os.CreateTemp` in that directory, applies `0600`, closes it, and renames it over the final path. Remove the temporary file on every failure path.

- [ ] **Step 4: Write failing resolver policy tests**

Define a fake discoverer and cover the state transitions:

```go
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
	r := Resolver{
		Now: func() time.Time { return now },
		Discover: func(context.Context, Family, []string) (string, error) {
			return "", errors.New("offline")
		},
	}
	got := r.Resolve(context.Background(), Options{Enabled: true, RefreshInterval: 6 * time.Hour, CachePath: path})
	if got.IPv4 != "8.8.8.8" {
		t.Fatalf("result=%+v", got)
	}
}
```

Also cover first-run discovery, independent IPv4/IPv6 success, disabled discovery, corrupt-cache recovery, successful stale replacement, and cache-write failure still returning the fresh result.

- [ ] **Step 5: Implement the resolver**

Create these definitions:

```go
type Options struct {
	Enabled         bool
	RefreshInterval time.Duration
	Timeout         time.Duration
	CachePath       string
	IPv4Endpoints   []string
	IPv6Endpoints   []string
	Logf            func(string, ...any)
}

type Result struct {
	IPv4 string
	IPv6 string
}

type DiscoverFunc func(context.Context, Family, []string) (string, error)

type Resolver struct {
	Discover DiscoverFunc
	Now      func() time.Time
}

func (r Resolver) Resolve(ctx context.Context, opts Options) Result
```

Resolve each family independently. A fresh entry is returned without network
access. Stale/absent IPv4 and IPv6 refreshes run concurrently, and each call is
wrapped in its own `context.WithTimeout(ctx, opts.Timeout)` so all configured
fallback endpoints share the three-second family budget. Success updates that
family and its timestamp, while failure returns the stale validated value. Log
only the family and safe error category, never endpoint response content.

- [ ] **Step 6: Run all egress package tests**

Run: `go test ./internal/egressip`

Expected: PASS.

- [ ] **Step 7: Write a failing Agent integration test with an injected resolver**

Add an internal dependency seam and test it without network access:

```go
func TestBuildReportsResolvedEgressIPs(t *testing.T) {
	cfg := config.Default()
	cfg.Detect.CustomDir = t.TempDir()
	cfg.Sinks = []config.Sink{{Type: "webhook", URL: "https://example.com/report"}}
	rep, err := buildWithEgress(context.Background(), cfg, "test", fakeRunner{}, nil,
		func(context.Context, egressip.Options) egressip.Result {
			return egressip.Result{IPv4: "8.8.8.8", IPv6: "2001:4860:4860::8888"}
		})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Host.EgressIPv4 != "8.8.8.8" || rep.Host.EgressIPv6 != "2001:4860:4860::8888" {
		t.Fatalf("host = %+v", rep.Host)
	}
}
```

- [ ] **Step 8: Run the Agent test and confirm the seam is missing**

Run: `go test ./internal/agent -run TestBuildReportsResolvedEgressIPs`

Expected: compile failure because `buildWithEgress` does not exist.

- [ ] **Step 9: Integrate resolution into report construction**

Add:

```go
type resolveEgressFunc func(context.Context, egressip.Options) egressip.Result

func buildWithEgress(
	ctx context.Context,
	cfg *config.Config,
	version string,
	runner detect.Runner,
	logf func(string, ...any),
	resolve resolveEgressFunc,
) (*report.Report, error)
```

Keep the existing `Build` and `build` signatures and have them call
`buildWithEgress` with a production resolver. Parse the already-validated
duration strings, pass both refresh interval and timeout through `Options`,
use `filepath.Join(ConfigDir(), "egress-ip-cache.json")`, create the Task 2
client with the configured timeout, and copy the result into
`host.EgressIPv4/EgressIPv6`. Existing Agent unit tests that do not exercise
this feature must explicitly set `cfg.Collect.EgressIP.Enabled = false` so
they remain hermetic.

- [ ] **Step 10: Run Agent and egress tests**

Run: `go test ./internal/egressip ./internal/agent`

Expected: PASS.

- [ ] **Step 11: Commit cache and Agent integration**

```bash
git add internal/egressip/cache.go internal/egressip/cache_test.go internal/egressip/resolver.go internal/egressip/resolver_test.go internal/agent/run.go internal/agent/run_test.go
git commit -m "feat: cache and report egress addresses"
```

---

### Task 4: Add Panel address semantics and database migration

**Files:**
- Create: `internal/panel/geo/address.go`
- Create: `internal/panel/geo/address_test.go`
- Modify: `internal/panel/db/db.go`
- Modify: `internal/panel/db/db_test.go`

**Interfaces:**
- Consumes: `report.Host.EgressIPv4/EgressIPv6` from Task 1.
- Produces: `geo.EffectiveIP(report.Host) string` and `geo.IsPublicIP(string) bool`.
- Produces: `nodes.ip_geo_ip` and `ip_geo_cache` for Tasks 6 and 7.

- [ ] **Step 1: Write failing address-selection tests**

```go
func TestEffectiveIPPrecedence(t *testing.T) {
	tests := []struct {
		name string
		host report.Host
		want string
	}{
		{"egress IPv4", report.Host{EgressIPv4: "8.8.8.8", EgressIPv6: "2001:4860:4860::8888", IPv4: "10.0.0.2"}, "8.8.8.8"},
		{"egress IPv6", report.Host{EgressIPv6: "2001:4860:4860::8888", IPv4: "10.0.0.2"}, "2001:4860:4860::8888"},
		{"legacy IPv4", report.Host{IPv4: "1.1.1.1", IPv6: "2001:4860:4860::8888"}, "1.1.1.1"},
		{"legacy IPv6", report.Host{IPv6: "2001:4860:4860::8888"}, "2001:4860:4860::8888"},
		{"empty", report.Host{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveIP(tt.host); got != tt.want {
				t.Fatalf("EffectiveIP() = %q", got)
			}
		})
	}
}

func TestIsPublicIPRejectsReservedRanges(t *testing.T) {
	for _, ip := range []string{"", "bad", "127.0.0.1", "10.0.0.1", "169.254.1.1", "224.0.0.1", "::1", "fe80::1", "ff02::1"} {
		if IsPublicIP(ip) {
			t.Fatalf("IsPublicIP(%q) = true", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "2001:4860:4860::8888"} {
		if !IsPublicIP(ip) {
			t.Fatalf("IsPublicIP(%q) = false", ip)
		}
	}
}
```

- [ ] **Step 2: Run tests and confirm the helpers are missing**

Run: `go test ./internal/panel/geo -run 'Test(EffectiveIPPrecedence|IsPublicIPRejectsReservedRanges)$'`

Expected: compile failure.

- [ ] **Step 3: Implement effective and public address helpers**

Use `netip.ParseAddr`, `Unmap`, `IsGlobalUnicast`, `IsPrivate`, `IsLoopback`, `IsLinkLocalUnicast`, `IsMulticast`, and `IsUnspecified`. `EffectiveIP` validates/canonicalizes each candidate and follows the exact Global Constraints precedence.

- [ ] **Step 4: Write failing migration assertions**

Extend `TestOpenAndMigrate`:

```go
for _, column := range []string{"ip_geo_ip", "ip_location", "ip_country", "ip_region", "ip_city", "ip_geo_updated_at"} {
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM pragma_table_info('nodes') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
		t.Fatalf("nodes.%s count=%d err=%v", column, count, err)
	}
}
var cacheTable int
if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='ip_geo_cache'`).Scan(&cacheTable); err != nil || cacheTable != 1 {
	t.Fatalf("ip_geo_cache count=%d err=%v", cacheTable, err)
}
```

Call `Migrate(d)` twice to prove idempotency.

- [ ] **Step 5: Run the migration test and confirm failure**

Run: `go test ./internal/panel/db -run TestOpenAndMigrate`

Expected: failure for missing `nodes.ip_geo_ip` or `ip_geo_cache`.

- [ ] **Step 6: Implement the additive migration**

Add `{name: "ip_geo_ip", def: "TEXT"}` to `addMissingNodeColumns`, add it to fresh `nodes` DDL, and add:

```sql
CREATE TABLE IF NOT EXISTS ip_geo_cache(
  ip TEXT PRIMARY KEY,
  location TEXT,
  country TEXT,
  region TEXT,
  city TEXT,
  updated_at INTEGER NOT NULL
);
```

- [ ] **Step 7: Run focused Panel tests**

Run: `go test ./internal/panel/geo ./internal/panel/db`

Expected: PASS.

- [ ] **Step 8: Commit address semantics and migration**

```bash
git add internal/panel/geo/address.go internal/panel/geo/address_test.go internal/panel/db/db.go internal/panel/db/db_test.go
git commit -m "feat: persist egress geolocation identity"
```

---

### Task 5: Replace plaintext geolocation with the HTTPS provider interface

**Files:**
- Modify: `internal/panel/config/config.go`
- Modify: `internal/panel/config/config_test.go`
- Modify: `internal/panel/geo/geo.go`
- Modify: `internal/panel/geo/geo_test.go`

**Interfaces:**
- Produces: `geo.Provider`, `geo.NewHTTPClient`, and `geo.NewIPWhoisProvider(client, urlTemplate, bearerToken)`.
- Produces: Panel Geo configuration used by Task 7 startup.
- Preserves: `geo.Location` and `geo.Format` used by Task 6.

- [ ] **Step 1: Write failing Panel Geo configuration tests**

```go
func TestGeoDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Geo.Provider != "ipwhois" || cfg.Geo.URL != "https://ipwho.is/{ip}?lang=zh-CN" || cfg.Geo.Timeout != "4s" || cfg.Geo.RefreshInterval != "12h" {
		t.Fatalf("geo defaults = %+v", cfg.Geo)
	}
}

func TestValidateRejectsUnsafeGeoURL(t *testing.T) {
	cfg := Default()
	cfg.Geo.URL = "http://geo.example/{ip}"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "geo url") {
		t.Fatalf("Validate() = %v", err)
	}
}
```

- [ ] **Step 2: Run configuration tests and confirm failure**

Run: `go test ./internal/panel/config -run 'Test(GeoDefaults|ValidateRejectsUnsafeGeoURL)$'`

Expected: compile failure for missing settings and `Validate`.

- [ ] **Step 3: Implement Panel Geo settings and validation**

Extend `Geo` with:

```go
Provider string `toml:"provider"`
URL      string `toml:"url"`
Timeout  string `toml:"timeout"`
TokenEnv string `toml:"token_env"`
```

Set the Global Constraints defaults. Add `func (c *Config) Validate() error` that accepts only provider `ipwhois`, requires exactly one `{ip}` placeholder, requires a positive timeout/refresh interval, and applies the same HTTPS-or-localhost rule as Agent endpoints. Call this validation from Panel startup in Task 7.

- [ ] **Step 4: Replace the old global lookup test with provider tests**

Use an injected client:

```go
func TestIPWhoisProviderLookup(t *testing.T) {
	var gotURL, gotAuth string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotAuth = req.Header.Get("Authorization")
		return response(http.StatusOK, `{"success":true,"country":"美国","region":"加利福尼亚州","city":"洛杉矶"}`), nil
	})}
	p := NewIPWhoisProvider(client, "https://geo.test/{ip}?lang=zh-CN", "secret")
	loc, err := p.Lookup(context.Background(), "2001:4860:4860::8888")
	if err != nil || Format(loc) != "美国-洛杉矶" {
		t.Fatalf("location=%+v err=%v", loc, err)
	}
	if gotURL != "https://geo.test/2001:4860:4860::8888?lang=zh-CN" || gotAuth != "Bearer secret" {
		t.Fatalf("url=%q auth=%q", gotURL, gotAuth)
	}
}

func TestIPWhoisProviderRejectsApplicationError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"success":false,"message":"rate limit"}`), nil
	})}
	_, err := NewIPWhoisProvider(client, "https://geo.test/{ip}", "").Lookup(context.Background(), "8.8.8.8")
	if err == nil {
		t.Fatal("expected provider error")
	}
}
```

Also test non-2xx, malformed JSON, an oversized body, and that the returned error excludes the provider response body.

Retain the existing `roundTripFunc` in `geo_test.go` and add the same explicit
`response(status int, body string) *http.Response` helper shown in Task 2 to
that package's test file.

- [ ] **Step 5: Run provider tests and confirm the old API does not match**

Run: `go test ./internal/panel/geo -run 'Test(IPWhoisProvider|Format)'`

Expected: compile failure for missing constructor/interface.

- [ ] **Step 6: Implement the provider interface and adapter**

Use:

```go
type Provider interface {
	Lookup(context.Context, string) (Location, error)
}

type IPWhoisProvider struct {
	client      *http.Client
	urlTemplate string
	bearerToken string
}

func NewIPWhoisProvider(client *http.Client, urlTemplate, bearerToken string) *IPWhoisProvider
func NewHTTPClient(timeout time.Duration) *http.Client
func (p *IPWhoisProvider) Lookup(ctx context.Context, ip string) (Location, error)
```

Validate the IP again, substitute only the canonical address for `{ip}`, send `Authorization: Bearer ...` only when non-empty, require 2xx, limit JSON to 1 MiB, require `success: true`, and map `country`, `region`, and `city` into the existing `Location`. Remove the plaintext hard-coded `Lookup` and global mutable HTTP client.

`NewHTTPClient` applies the configured timeout, stops after three redirects,
and rejects HTTPS-to-HTTP redirect downgrades. Panel startup must use this
constructor rather than `http.DefaultClient`.

- [ ] **Step 7: Run Panel config and provider tests**

Run: `go test ./internal/panel/config ./internal/panel/geo`

Expected: PASS.

- [ ] **Step 8: Commit provider hardening**

```bash
git add internal/panel/config/config.go internal/panel/config/config_test.go internal/panel/geo/geo.go internal/panel/geo/geo_test.go
git commit -m "feat: use HTTPS geolocation provider"
```

---

### Task 6: Implement immediate, cached, race-safe Panel refresh

**Files:**
- Create: `internal/panel/geo/refresher.go`
- Create: `internal/panel/geo/refresher_test.go`

**Interfaces:**
- Consumes: `geo.Provider`, `geo.EffectiveIP`, `geo.IsPublicIP`, `nodes.ip_geo_ip`, and `ip_geo_cache`.
- Produces: `geo.Refresher`, `NewRefresher`, `Run`, `ObserveNode`, and `Reconcile` for Task 7.

- [ ] **Step 1: Write a deterministic fake provider**

```go
type fakeProvider struct {
	mu        sync.Mutex
	locations map[string]Location
	calls     []string
	block     map[string]chan struct{}
}

func (p *fakeProvider) Lookup(_ context.Context, ip string) (Location, error) {
	p.mu.Lock()
	p.calls = append(p.calls, ip)
	ch := p.block[ip]
	loc, ok := p.locations[ip]
	p.mu.Unlock()
	if ch != nil {
		<-ch
	}
	if !ok {
		return Location{}, errors.New("not found")
	}
	return loc, nil
}
```

Define the database fixtures used below:

```go
func openGeoTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := paneldb.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := paneldb.Migrate(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func insertNode(t *testing.T, d *sql.DB, nodeID string, host report.Host) {
	t.Helper()
	b, err := json.Marshal(host)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := d.Exec(`INSERT INTO nodes(node_id,last_host_json,last_services_json,created_at,updated_at) VALUES(?,?,'[]',?,?)`, nodeID, string(b), now, now); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Write failing immediate-observation and cache tests**

```go
func TestObserveNodeQueuesNewPublicIP(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	p := &fakeProvider{locations: map[string]Location{"8.8.8.8": {Country: "美国", City: "山景城"}}}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	if err := r.ObserveNode(context.Background(), "n1", report.Host{EgressIPv4: "8.8.8.8"}); err != nil {
		t.Fatal(err)
	}
	if err := r.refreshOne(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	var geoIP, location string
	if err := d.QueryRow(`SELECT ip_geo_ip, ip_location FROM nodes WHERE node_id='n1'`).Scan(&geoIP, &location); err != nil {
		t.Fatal(err)
	}
	if geoIP != "8.8.8.8" || location != "美国-山景城" {
		t.Fatalf("geoIP=%q location=%q", geoIP, location)
	}
}

func TestRefreshOneReusesPersistentCache(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	_, err := d.Exec(`INSERT INTO ip_geo_cache(ip,location,country,region,city,updated_at) VALUES(?,?,?,?,?,?)`,
		"8.8.8.8", "美国-山景城", "美国", "加利福尼亚州", "山景城", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{locations: map[string]Location{}}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	if err := r.refreshOne(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 0 {
		t.Fatalf("provider calls = %v", p.calls)
	}
}
```

Add tests that private/loopback addresses become `内网` without provider access, no address clears geography, an IP change clears the old location immediately, repeated queue calls deduplicate, and queue saturation is non-blocking.

- [ ] **Step 3: Write the stale-result race test**

Start a blocked lookup for `8.8.8.8`, call `ObserveNode` with `1.1.1.1`, release the old lookup, and assert the final update guarded by `WHERE ip_geo_ip = ?` does not write the old location onto the new address.

- [ ] **Step 4: Run refresher tests and confirm the implementation is missing**

Run: `go test ./internal/panel/geo -run 'Test(ObserveNode|RefreshOne|OldLookup|Queue)'`

Expected: compile failure for missing `Refresher`.

- [ ] **Step 5: Implement observation and the bounded queue**

Create:

```go
type Refresher struct {
	db       *sql.DB
	provider Provider
	ttl      time.Duration
	queue    chan string
	mu       sync.Mutex
	pending  map[string]struct{}
	logf     func(string, ...any)
	now      func() time.Time
}

func NewRefresher(d *sql.DB, provider Provider, ttl time.Duration, logf func(string, ...any)) *Refresher
func (r *Refresher) Run(ctx context.Context)
func (r *Refresher) ObserveNode(ctx context.Context, nodeID string, host report.Host) error
func (r *Refresher) Reconcile(ctx context.Context) error
```

Use a queue capacity of 128. `enqueue` inserts into `pending` under the mutex, sends without blocking, and removes the pending marker if the channel is full. `Run` processes one IP at a time and always removes its marker after `refreshOne` returns.

`ObserveNode` canonicalizes with `EffectiveIP`. For an empty address, clear `ip_geo_ip` and all location fields. For a non-public legacy fallback, set `ip_geo_ip`, `ip_location='内网'`, empty country/region/city, and current `ip_geo_updated_at`. For a public address, atomically set the new `ip_geo_ip` and clear stale geography only when the address changed; enqueue when changed, missing, or older than the TTL.

- [ ] **Step 6: Implement persistent cache lookup and conditional fan-out**

`refreshOne` first reads a fresh `ip_geo_cache` row. On a miss/stale row it calls the provider and upserts:

```sql
INSERT INTO ip_geo_cache(ip,location,country,region,city,updated_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(ip) DO UPDATE SET
  location=excluded.location,
  country=excluded.country,
  region=excluded.region,
  city=excluded.city,
  updated_at=excluded.updated_at;
```

Then update all still-matching nodes:

```sql
UPDATE nodes
SET ip_location=?, ip_country=?, ip_region=?, ip_city=?, ip_geo_updated_at=?
WHERE ip_geo_ip=?;
```

This `WHERE` clause is the race guard. Do not clear an existing location merely because a scheduled refresh of the same IP fails; only an actual IP change clears stale geography.

- [ ] **Step 7: Implement reconciliation without holding rows open**

Read `node_id` and `last_host_json` into a slice, close/check the rows, then call `ObserveNode` for each decoded Host. Invalid legacy JSON is logged and skipped. Do not call the provider inside `Reconcile`; all network work stays in `Run`.

- [ ] **Step 8: Run refresher tests with the race detector**

Run: `go test -race ./internal/panel/geo`

Expected: PASS and no race reports.

- [ ] **Step 9: Commit the worker**

```bash
git add internal/panel/geo/refresher.go internal/panel/geo/refresher_test.go
git commit -m "feat: refresh node geolocation immediately"
```

---

### Task 7: Wire report ingestion and Panel lifecycle to the worker

**Files:**
- Modify: `internal/panel/api/api.go`
- Modify: `internal/panel/api/report.go`
- Modify: `internal/panel/api/report_test.go`
- Modify: `cmd/net-probe-panel/main.go`

**Interfaces:**
- Consumes: `geo.Refresher` from Task 6 and Panel Geo configuration from Task 5.
- Produces: immediate non-blocking observation on every accepted report.
- Preserves: `api.New(d, cfg)` compatibility in tests by using an optional observer argument.

- [ ] **Step 1: Write a failing report-observer test**

```go
type fakeGeoObserver struct {
	nodeID string
	host   report.Host
	err    error
}

func (o *fakeGeoObserver) ObserveNode(_ context.Context, nodeID string, host report.Host) error {
	o.nodeID, o.host = nodeID, host
	return o.err
}

func TestHandleReportObservesEgressIP(t *testing.T) {
	d, cfg := openTestDB(t)
	observer := &fakeGeoObserver{}
	s := New(d, cfg, observer)
	body := `{"schema_version":"1","node_id":"n1","host":{"egress_ipv4":"8.8.8.8"},"services":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/report", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rr := httptest.NewRecorder()
	s.handleReport(rr, req)
	if rr.Code != http.StatusOK || observer.nodeID != "n1" || observer.host.EgressIPv4 != "8.8.8.8" {
		t.Fatalf("code=%d observer=%+v", rr.Code, observer)
	}
}
```

Add a test where the observer returns an error and the handler still returns `200 {"ack":true...}` after persisting the report.

Add an end-to-end package test using a real `geo.Refresher` and a blocking fake
`geo.Provider`: start `Refresher.Run` in a cancellable goroutine, POST a report,
assert the ACK arrives while the provider is still blocked, release it, and
poll SQLite for at most one second until `ip_location` equals `美国-山景城`.
Name this test `TestReportToGeoIntegration`.

- [ ] **Step 2: Run report tests and confirm the constructor does not accept the observer**

Run: `go test ./internal/panel/api -run 'TestHandleReport(ObservesEgressIP|ObserverFailureStillAcknowledges)$'`

Expected: compile failure.

- [ ] **Step 3: Add the optional observer interface and ingestion call**

In `api.go` add:

```go
type NodeGeoObserver interface {
	ObserveNode(context.Context, string, report.Host) error
}

type Server struct {
	db          *sql.DB
	cfg         *config.Config
	geoObserver NodeGeoObserver
	ConfigPath  string
}

func New(d *sql.DB, cfg *config.Config, observers ...NodeGeoObserver) *Server
```

Use only the first optional observer. In `handleReport`, call it after both database inserts and before writing the ACK. Log a safe node-scoped error and continue on failure; never return a non-2xx response because geolocation failed.

- [ ] **Step 4: Run API tests**

Run: `go test ./internal/panel/api`

Expected: PASS.

- [ ] **Step 5: Replace Panel startup geolocation wiring**

After config load, call `cfg.Validate()` and fail startup on invalid secure transport settings. Resolve `cfg.Geo.TokenEnv` from the environment without logging its value. Parse timeout/freshness, construct an HTTP client with redirect downgrade protection, create `geo.NewIPWhoisProvider`, then create `geo.NewRefresher`.

Start `go refresher.Run(ctx)` before serving requests, pass it to `api.New(d, cfg, refresher)`, and change `startBackground` to accept the refresher. Replace `refreshIPLocations` network work with:

```go
go runEvery(ctx, geoRefreshInterval(cfg), func(ctx context.Context) {
	if err := refresher.Reconcile(ctx); err != nil {
		log.Printf("IP location reconciliation: %v", err)
	}
})
```

Delete the old row-by-row `geo.Lookup` implementation from `main.go`; `Reconcile` now owns that responsibility.

- [ ] **Step 6: Run all Panel tests**

Run: `go test ./internal/panel/... ./cmd/net-probe-panel`

Expected: PASS.

- [ ] **Step 7: Run Panel vet**

Run: `go vet ./internal/panel/... ./cmd/net-probe-panel`

Expected: no output and exit 0.

- [ ] **Step 8: Commit Panel integration**

```bash
git add internal/panel/api/api.go internal/panel/api/report.go internal/panel/api/report_test.go cmd/net-probe-panel/main.go
git commit -m "feat: trigger geolocation from agent reports"
```

---

### Task 8: Correct frontend address and geography display

**Files:**
- Create: `web/src/lib/host.ts`
- Create: `web/src/lib/host.test.ts`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/Nodes.tsx`
- Modify: `web/src/pages/NodeDetail.tsx`

**Interfaces:**
- Consumes: optional Host fields and existing `Node.ip_location`.
- Produces: `egressIP(host: Host): string` as the only frontend precedence rule.

- [ ] **Step 1: Add the optional TypeScript fields and failing helper tests**

Add to `Host`:

```ts
egress_ipv4?: string;
egress_ipv6?: string;
```

Create:

```ts
import { describe, expect, it } from "vitest";
import { egressIP } from "./host";
import type { Host } from "./api";

function host(overrides: Partial<Host>): Host {
  return {
    hostname: "n1", os: "Linux", os_version: "", kernel: "", arch: "amd64",
    uptime_seconds: 0, load1: 0, load5: 0, load15: 0,
    mem_total_bytes: 0, mem_available_bytes: 0, mem_used_pct: 0,
    disk_used_pct: 0, upgradable_count: 0,
    ...overrides,
  };
}

describe("egressIP", () => {
  it("prefers discovered IPv4 over all other addresses", () => {
    expect(egressIP(host({ egress_ipv4: "8.8.8.8", egress_ipv6: "2001:4860:4860::8888", ipv4: "10.0.0.2" }))).toBe("8.8.8.8");
  });

  it("falls back through discovered IPv6 and legacy Agent fields", () => {
    expect(egressIP(host({ egress_ipv6: "2001:4860:4860::8888", ipv4: "10.0.0.2" }))).toBe("2001:4860:4860::8888");
    expect(egressIP(host({ ipv4: "1.1.1.1", ipv6: "2001:4860:4860::8888" }))).toBe("1.1.1.1");
    expect(egressIP(host({ ipv6: "2001:4860:4860::8888" }))).toBe("2001:4860:4860::8888");
    expect(egressIP(host({}))).toBe("—");
  });
});
```

- [ ] **Step 2: Run the helper test and confirm the module is missing**

Run: `npm --prefix web test -- --run src/lib/host.test.ts`

Expected: failure resolving `./host`.

- [ ] **Step 3: Implement the shared helper**

```ts
import type { Host } from "./api";

export function egressIP(host: Host): string {
  return host.egress_ipv4 || host.egress_ipv6 || host.ipv4 || host.ipv6 || "—";
}
```

- [ ] **Step 4: Update node table and cards**

Import `egressIP`. In the table rename `IP` to `出口 IP`, render `egressIP(n.host)`, rename `IP出口地址` to `国家/地区`, and keep `n.ip_location || "—"`.

In cards use `egressIP(n.host)` below the node name. Replace the single ambiguous definition row with two rows:

```tsx
<div className="flex justify-between gap-2">
  <dt>出口 IP</dt>
  <dd className="truncate text-fg">{egressIP(n.host)}</dd>
</div>
<div className="flex justify-between gap-2">
  <dt>国家/地区</dt>
  <dd className="truncate text-fg">{n.ip_location || "—"}</dd>
</div>
```

- [ ] **Step 5: Update node detail summary**

Import `egressIP`, change the current `IPv4` card to `出口 IP`, and add a `国家/地区` card using `node.ip_location || "—"`. Use `lg:grid-cols-5` so the five summary cards remain on one row at large widths.

- [ ] **Step 6: Run frontend unit tests and type checking**

Run: `npm --prefix web test`

Expected: PASS.

Run: `npm --prefix web run typecheck`

Expected: PASS.

- [ ] **Step 7: Build and check the frontend bundle**

Run: `npm --prefix web run build`

Expected: Vite build succeeds.

Run: `npm --prefix web run check:bundle`

Expected: bundle size gate passes.

- [ ] **Step 8: Commit frontend semantics**

```bash
git add web/src/lib/api.ts web/src/lib/host.ts web/src/lib/host.test.ts web/src/pages/Nodes.tsx web/src/pages/NodeDetail.tsx
git commit -m "feat: display node egress IP and region"
```

---

### Task 9: Document, verify, and stage the complete feature

**Files:**
- Modify: `README.md`
- Verify: all files from Tasks 1–8

**Interfaces:**
- Consumes: the completed Agent, Panel, and frontend behavior.
- Produces: operator-facing configuration and end-to-end evidence.

- [ ] **Step 1: Document Agent discovery in both README languages**

Add the exact default section to both minimal configuration examples:

```toml
[collect.egress_ip]
enabled = true
refresh_interval = "6h"
timeout = "3s"
ipv4_endpoints = ["https://api.ipify.org", "https://4.ident.me"]
ipv6_endpoints = ["https://api6.ipify.org", "https://6.ident.me"]
```

Explain that omission uses these defaults, successful values are cached in `/etc/net-probe/egress-ip-cache.json`, and a provider failure does not stop reporting.

- [ ] **Step 2: Document Panel provider configuration in both README languages**

Add:

```toml
[geo]
refresh_interval = "12h"
provider = "ipwhois"
url = "https://ipwho.is/{ip}?lang=zh-CN"
timeout = "4s"
token_env = ""
```

Explain the 1,000-request/day default-provider allowance, SQLite cache/reconciliation behavior, and the environment-variable mechanism for compatible bearer-token providers. Do not include any actual token.

- [ ] **Step 3: Run formatters**

Run: `gofmt -w internal/report/types.go internal/report/types_test.go internal/config/config.go internal/config/config_test.go internal/egressip internal/agent/run.go internal/agent/run_test.go internal/panel/config internal/panel/db internal/panel/geo internal/panel/api cmd/net-probe-panel/main.go`

Expected: exit 0.

- [ ] **Step 4: Run the complete Go verification**

Run: `go test -race ./...`

Expected: PASS.

Run: `go vet ./...`

Expected: no output and exit 0.

- [ ] **Step 5: Run the complete frontend verification**

Run: `npm --prefix web run verify`

Expected: Vitest, TypeScript, Vite build, and bundle gate all pass.

- [ ] **Step 6: Check diffs and binary sizes**

Run: `git diff --check`

Expected: no output.

Build release-style Linux binaries with the exact workflow flags and record raw
and gzip-compressed sizes:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=egress-ip-verification" -o /tmp/net-probe_linux_amd64 ./cmd/net-probe
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=egress-ip-verification" -o /tmp/net-probe-panel_linux_amd64 ./cmd/net-probe-panel
gzip -9 -c /tmp/net-probe_linux_amd64 > /tmp/net-probe_linux_amd64.gz
gzip -9 -c /tmp/net-probe-panel_linux_amd64 > /tmp/net-probe-panel_linux_amd64.gz
wc -c /tmp/net-probe_linux_amd64 /tmp/net-probe_linux_amd64.gz /tmp/net-probe-panel_linux_amd64 /tmp/net-probe-panel_linux_amd64.gz
```

This feature adds no third-party runtime dependency. Report the exact numbers;
do not claim a repository size gate that does not exist.

- [ ] **Step 7: Commit documentation**

```bash
git add README.md
git commit -m "docs: explain egress IP discovery"
```

- [ ] **Step 8: Perform local API integration verification**

Run the deterministic integration test added in Task 7:

```bash
go test -race ./internal/panel/api -run TestReportToGeoIntegration -count=1 -v
```

Expected: PASS. The test posts an authenticated report containing
`"egress_ipv4":"8.8.8.8"` and verifies all of these stored values:

```json
{
  "host": {"egress_ipv4": "8.8.8.8"},
  "ip_location": "美国-山景城",
  "ip_country": "美国",
  "ip_region": "加利福尼亚州",
  "ip_city": "山景城"
}
```

The location must appear before the 12-hour periodic interval and the Agent report request must receive its ACK before the fake provider is released.

- [ ] **Step 9: Perform staging verification on the existing test server**

Deploy the new Panel first, then the Agent. Do not print credentials or tokens. Verify:

```bash
sudo systemctl restart net-probe-panel
sudo systemctl start net-probe.service
sudo journalctl -u net-probe.service -n 30 --no-pager
sudo stat -c '%a %n' /etc/net-probe/egress-ip-cache.json
```

Expected: Agent exits successfully, cache mode is `600`, the Panel node API contains the public `egress_ipv4`, and `ip_location` becomes non-empty within one worker cycle rather than after 12 hours.

- [ ] **Step 10: Record final evidence and stop for release approval**

Report the commit range, exact verification commands and results, Agent/Panel artifact sizes, and staging node values with credentials redacted. Do not merge or tag a release until the user explicitly approves that next action.
