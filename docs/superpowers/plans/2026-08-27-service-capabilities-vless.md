# Service Capabilities and VLESS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make service telemetry self-describing, preserve successful zero values, explain unavailable metrics, and display VLESS as a protocol tag on Xray and sing-box service instances.

**Architecture:** The Agent owns per-service capability descriptors and emits independent traffic and online-client observations. VLESS discovery reads only effective Xray/sing-box configuration metadata and emits a normalized protocol tag; the Panel stores the additive JSON unchanged, normalizes legacy reports at render time, and aggregates only successful observations with explicit coverage.

**Tech Stack:** Go 1.23, YAML v3 already present in the Agent, React 18, TypeScript 5.6, Vite 5, Vitest 2.1.9, Recharts, SQLite JSON storage

## Global Constraints

- Keep `schema_version: "1"`; all report-contract changes are additive.
- Continue emitting legacy `service.stats` for old Panels until a future schema version removes it.
- New Panels must accept v0.1.0 reports without capability, telemetry, or protocol fields.
- Statistics remain service-instance aggregates; do not add per-inbound, per-protocol, or per-user metrics.
- Do not estimate traffic or online clients from NIC counters, sockets, firewall counters, cgroups, or eBPF.
- VLESS is a protocol tag on Xray or sing-box, never a duplicate service.
- Never upload UUIDs, passwords, domains, TLS material, transport paths, raw endpoints, authorization headers, command output, or complete configuration fragments.
- Telemetry availability must not change `service.status` or existing service-health alerts.
- Do not add a SQL migration; persist the additive fields through existing `services_json` columns.
- Do not add an Agent runtime dependency; reuse the standard library and the existing YAML v3 parser.
- Keep the existing frontend bundle gate at 512,000 bytes per JavaScript chunk.

---

## File Structure

### Agent report and collection

- `internal/report/types.go`: wire-format types, enums, and legacy-stat projection.
- `internal/report/types_test.go`: JSON contract tests, including successful zero values.
- `internal/detect/capabilities.go`: collector-owned capability descriptors.
- `internal/detect/capabilities_test.go`: capability-matrix tests.
- `internal/detect/stats.go`: native metric collection and safe error-code mapping.
- `internal/detect/stats_test.go`: independent traffic/online observations and failure mapping.
- `internal/config/config.go`: explicit statistics enable/disable control.
- `internal/config/config_test.go`: TOML compatibility tests for the optional control.
- `internal/detect/detect.go`: combine capabilities, observations, and legacy stats without changing service health.
- `internal/detect/detect_test.go`: orchestration behavior for unsupported, disabled, and unconfigured services.
- `internal/agent/run.go`: route collector diagnostics to the configured local logger without adding them to reports.
- `internal/agent/run_test.go`: verify diagnostic logging stays local.

### Protocol discovery

- `internal/detect/systemd.go`: retain parsed `ExecStart` arguments as well as the binary path.
- `internal/detect/systemd_test.go`: `ExecStart` argument parsing tests.
- `internal/detect/protocols.go`: effective config-path resolution and VLESS-only discovery.
- `internal/detect/protocols_test.go`: Xray/sing-box discovery, deduplication, failure isolation, and secret-leak tests.

### Panel

- `web/package.json`, `web/package-lock.json`: add Vitest and include tests in `verify`.
- `web/src/lib/api.ts`: additive TypeScript wire types.
- `web/src/lib/serviceTelemetry.ts`: legacy normalization and per-metric display state.
- `web/src/lib/serviceTelemetry.test.ts`: state and compatibility tests.
- `web/src/components/ServiceCard.tsx`: service title, VLESS tag, and metric rows.
- `web/src/components/ServiceCard.test.tsx`: server-rendered service-card assertions.
- `web/src/lib/traffic.ts`: capability-aware historical aggregation.
- `web/src/lib/traffic.test.ts`: zero, gap, coverage, and empty-state tests.
- `web/src/pages/NodeDetail.tsx`: use the new service card and traffic aggregation.

### API, integration, and documentation

- `internal/panel/api/report_test.go`: legacy and extended report persistence tests.
- `internal/agent/integration_test.go`: extended report and sensitive-data boundary test.
- `README.md`: explain implementation versus protocol, telemetry states, VLESS tags, and explicit statistics disablement.

---

### Task 1: Add the additive report contract

**Files:**
- Modify: `internal/report/types.go`
- Modify: `internal/report/types_test.go`

**Interfaces:**
- Produces: `CapabilitySupport`, `MetricCapability`, `ServiceCapabilities`, `ObservationState`, `TrafficTelemetry`, `CountTelemetry`, `ServiceTelemetry`, `ProtocolInfo`.
- Produces: `func (s *Service) PopulateLegacyStats()`.
- Preserves: existing `Stats` and `Service.Stats` JSON fields.

- [ ] **Step 1: Write failing JSON contract tests**

Add tests that require successful zero values to remain present and require legacy projection:

```go
func uint64ptr(v uint64) *uint64 { return &v }

func TestTelemetryMarshalPreservesSuccessfulZero(t *testing.T) {
	svc := Service{
		Type: "xray",
		Capabilities: &ServiceCapabilities{
			Traffic: MetricCapability{Support: CapabilitySupported, Source: "native_api"},
			OnlineClients: MetricCapability{Support: CapabilitySupported, Source: "native_api"},
		},
		Telemetry: &ServiceTelemetry{
			Traffic: &TrafficTelemetry{State: ObservationOK, TxBytes: uint64ptr(0), RxBytes: uint64ptr(0)},
			OnlineClients: &CountTelemetry{State: ObservationOK, Value: uint64ptr(0)},
		},
	}
	b, err := json.Marshal(svc)
	if err != nil { t.Fatal(err) }
	for _, want := range []string{`"tx_bytes":0`, `"rx_bytes":0`, `"value":0`} {
		if !strings.Contains(string(b), want) { t.Fatalf("json %s missing %s", b, want) }
	}
}

func TestPopulateLegacyStats(t *testing.T) {
	svc := Service{Telemetry: &ServiceTelemetry{
		Traffic: &TrafficTelemetry{State: ObservationOK, TxBytes: uint64ptr(7), RxBytes: uint64ptr(9)},
		OnlineClients: &CountTelemetry{State: ObservationOK, Value: uint64ptr(0)},
	}}
	svc.PopulateLegacyStats()
	if svc.Stats == nil || svc.Stats.Tx != 7 || svc.Stats.Rx != 9 || svc.Stats.OnlineClients != 0 {
		t.Fatalf("legacy stats = %+v", svc.Stats)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/report -run 'TestTelemetryMarshalPreservesSuccessfulZero|TestPopulateLegacyStats'`

Expected: FAIL because the new types and fields do not exist.

- [ ] **Step 3: Add exact report types and constants**

Add the following wire model to `internal/report/types.go`:

```go
type CapabilitySupport string

const (
	CapabilitySupported   CapabilitySupport = "supported"
	CapabilityUnsupported CapabilitySupport = "unsupported"
	CapabilityUnknown     CapabilitySupport = "unknown"
)

type MetricCapability struct {
	Support    CapabilitySupport `json:"support"`
	Source     string            `json:"source,omitempty"`
	ReasonCode string            `json:"reason_code,omitempty"`
}

type ServiceCapabilities struct {
	Traffic       MetricCapability `json:"traffic"`
	OnlineClients MetricCapability `json:"online_clients"`
}

type ObservationState string

const (
	ObservationOK            ObservationState = "ok"
	ObservationNotConfigured ObservationState = "not_configured"
	ObservationDisabled      ObservationState = "disabled"
	ObservationError         ObservationState = "error"
)

type TrafficTelemetry struct {
	State     ObservationState `json:"state"`
	TxBytes   *uint64          `json:"tx_bytes,omitempty"`
	RxBytes   *uint64          `json:"rx_bytes,omitempty"`
	ErrorCode string           `json:"error_code,omitempty"`
}

type CountTelemetry struct {
	State     ObservationState `json:"state"`
	Value     *uint64          `json:"value,omitempty"`
	ErrorCode string           `json:"error_code,omitempty"`
}

type ServiceTelemetry struct {
	Traffic       *TrafficTelemetry `json:"traffic,omitempty"`
	OnlineClients *CountTelemetry   `json:"online_clients,omitempty"`
}

type ProtocolInfo struct {
	State  string   `json:"state"`
	Items  []string `json:"items"`
	Source string   `json:"source,omitempty"`
}
```

Extend `Service` with optional `Protocols`, `Capabilities`, and `Telemetry` pointers. Implement `PopulateLegacyStats` so it copies only successful observations, creates `Stats` when at least one observation succeeded, and never converts an error or missing observation to a fabricated value.

- [ ] **Step 4: Run report tests**

Run: `gofmt -w internal/report/types.go internal/report/types_test.go && go test ./internal/report`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/types.go internal/report/types_test.go
git commit -m "feat: add service capability report contract"
```

---

### Task 2: Add collector-owned capability descriptors

**Files:**
- Create: `internal/detect/capabilities.go`
- Create: `internal/detect/capabilities_test.go`

**Interfaces:**
- Consumes: `report.ServiceCapabilities` from Task 1.
- Produces: `func CapabilitiesFor(serviceType string) report.ServiceCapabilities`.

- [ ] **Step 1: Write the failing table test**

```go
func TestCapabilitiesFor(t *testing.T) {
	tests := []struct {
		service, traffic, online, trafficReason, onlineReason string
	}{
		{"hysteria2", "supported", "supported", "", ""},
		{"xray", "supported", "supported", "", ""},
		{"sing-box", "supported", "unsupported", "", "native_api_unavailable"},
		{"anytls", "unsupported", "unsupported", "native_api_unavailable", "native_api_unavailable"},
		{"v2ray", "unsupported", "unsupported", "collector_not_implemented", "collector_not_implemented"},
		{"shadowsocks", "unsupported", "unsupported", "collector_not_implemented", "collector_not_implemented"},
		{"trojan", "unsupported", "unsupported", "collector_not_implemented", "collector_not_implemented"},
		{"tuic", "unsupported", "unsupported", "collector_not_implemented", "collector_not_implemented"},
		{"custom", "unknown", "unknown", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			got := CapabilitiesFor(tt.service)
			if string(got.Traffic.Support) != tt.traffic || string(got.OnlineClients.Support) != tt.online {
				t.Fatalf("capabilities = %+v", got)
			}
			if got.Traffic.ReasonCode != tt.trafficReason || got.OnlineClients.ReasonCode != tt.onlineReason {
				t.Fatalf("reasons = %+v", got)
			}
		})
	}
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/detect -run TestCapabilitiesFor`

Expected: FAIL because `CapabilitiesFor` does not exist.

- [ ] **Step 3: Implement a closed descriptor map**

Implement `CapabilitiesFor` with a package-level map for the eight known service implementations and a default pair of `unknown` capabilities. Use `source: "native_api"` only for supported metrics. Do not derive capabilities from the Panel or from a protocol tag.

```go
func supported() report.MetricCapability {
	return report.MetricCapability{Support: report.CapabilitySupported, Source: "native_api"}
}

func unsupported(reason string) report.MetricCapability {
	return report.MetricCapability{Support: report.CapabilityUnsupported, ReasonCode: reason}
}
```

- [ ] **Step 4: Run tests**

Run: `gofmt -w internal/detect/capabilities.go internal/detect/capabilities_test.go && go test ./internal/detect -run TestCapabilitiesFor`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/detect/capabilities.go internal/detect/capabilities_test.go
git commit -m "feat: declare per-service telemetry capabilities"
```

---

### Task 3: Refactor native collectors into independent observations

**Files:**
- Modify: `internal/detect/stats.go`
- Modify: `internal/detect/stats_test.go`

**Interfaces:**
- Consumes: observation types from Task 1.
- Produces: `TelemetryDiagnostic`, `TelemetryResult`, and `func CollectTelemetryWithRunner(ctx context.Context, kind, endpoint, secret string, r Runner) TelemetryResult`.
- Produces: safe error codes `timeout`, `unauthorized`, `connection_failed`, `invalid_response`, `command_failed`, and `unknown`.

- [ ] **Step 1: Replace aggregate-stat tests with independent-observation tests**

Add focused tests proving:

```go
func TestHysteria2TelemetryKeepsTrafficWhenOnlineFails(t *testing.T) {
	withRoundTripper(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/traffic" {
			return jsonResponse(http.StatusOK, `{"u":{"tx":0,"rx":0}}`), nil
		}
		return jsonResponse(http.StatusUnauthorized, `denied`), nil
	})
	result := CollectTelemetryWithRunner(context.Background(), "hysteria2", "http://unused", "", fakeRunner{})
	got := result.Telemetry
	if got.Traffic.State != report.ObservationOK || *got.Traffic.TxBytes != 0 || *got.Traffic.RxBytes != 0 {
		t.Fatalf("traffic = %+v", got.Traffic)
	}
	if got.OnlineClients.State != report.ObservationError || got.OnlineClients.ErrorCode != "unauthorized" {
		t.Fatalf("online = %+v", got.OnlineClients)
	}
}

func TestSingBoxTelemetryHasNoOnlineObservation(t *testing.T) {
	withRoundTripper(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"up":7,"down":9}`), nil
	})
	result := CollectTelemetryWithRunner(context.Background(), "sing-box", "http://unused", "", fakeRunner{})
	got := result.Telemetry
	if got.Traffic.State != report.ObservationOK || got.OnlineClients != nil {
		t.Fatalf("telemetry = %+v", got)
	}
}
```

Define `withRoundTripper` and `jsonResponse` in the test file so transport replacement is restored with `t.Cleanup`.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/detect -run 'TestHysteria2TelemetryKeepsTrafficWhenOnlineFails|TestSingBoxTelemetryHasNoOnlineObservation'`

Expected: FAIL because collection currently returns one all-or-nothing `Stats` object.

- [ ] **Step 3: Implement independent collection**

Refactor Hysteria2 so `/traffic` and `/online` are requested independently. Refactor Xray so `stats query` and `statsonlineiplist` are independent commands. sing-box returns only a traffic observation. Each successful result stores pointers to values so zero is serialized.

Return report-safe observations separately from local-only diagnostics:

```go
type TelemetryDiagnostic struct {
	Metric string
	Err    error
}

type TelemetryResult struct {
	Telemetry   *report.ServiceTelemetry
	Diagnostics []TelemetryDiagnostic
}
```

Every failed observation appends one diagnostic containing the original error while its report observation contains only a stable `ErrorCode`.

Introduce a bounded internal error type:

```go
type telemetryError struct {
	code string
	err  error
}

func observationError(code string) *report.TrafficTelemetry {
	return &report.TrafficTelemetry{State: report.ObservationError, ErrorCode: code}
}
```

Use `errors.Is(err, context.DeadlineExceeded)`, typed HTTP status errors, JSON decode errors, and command failures to map to stable codes. Return raw errors only to local logs; never put raw error strings in `ServiceTelemetry`.

- [ ] **Step 4: Run all detector tests**

Run: `gofmt -w internal/detect/stats.go internal/detect/stats_test.go && go test ./internal/detect`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/detect/stats.go internal/detect/stats_test.go
git commit -m "feat: collect service telemetry independently"
```

---

### Task 4: Integrate capability and collection states into detection

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/detect/detect.go`
- Modify: `internal/detect/detect_test.go`
- Modify: `internal/agent/run.go`
- Modify: `internal/agent/run_test.go`

**Interfaces:**
- Consumes: `CapabilitiesFor` and `CollectTelemetryWithRunner`.
- Produces: `StatsService.Enabled *bool` with absent meaning automatic and `false` meaning disabled.
- Produces: every newly generated `report.Service` has capabilities; supported metrics have one observation per run.
- Produces: `Deps.Logf func(format string, args ...any)` for local-only collector diagnostics.

- [ ] **Step 1: Write failing configuration and orchestration tests**

Add a TOML test for explicit disablement:

```go
func TestStatsServiceCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `[agent]
node_id = "n1"
[[sink]]
type = "panel"
url = "https://panel.example"
[stats.services.xray]
enabled = false
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { t.Fatal(err) }
	cfg, err := Load(path)
	if err != nil { t.Fatal(err) }
	if cfg.Stats.Services["xray"].Enabled == nil || *cfg.Stats.Services["xray"].Enabled {
		t.Fatalf("enabled = %#v", cfg.Stats.Services["xray"].Enabled)
	}
}
```

Add detector tests for:

- AnyTLS: both capabilities unsupported and no telemetry observations.
- Xray without endpoint: both supported observations are `not_configured`.
- Xray with `enabled=false`: both supported observations are `disabled`.
- A telemetry error leaves `Service.Status` based only on active/listen/certificate state.
- Successful telemetry calls `PopulateLegacyStats`.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/config ./internal/detect -run 'TestStatsServiceCanBeDisabled|TestDetectCapabilities'`

Expected: FAIL because enablement and capability-aware orchestration do not exist.

- [ ] **Step 3: Add optional configuration control**

Extend the existing structure without changing defaults:

```go
type StatsService struct {
	Enabled  *bool  `toml:"enabled"`
	Endpoint string `toml:"endpoint"`
	Secret   string `toml:"secret"`
}
```

No service entry means automatic discovery. An entry with `enabled=false` suppresses collection and yields `disabled`; an entry with `enabled=true` but no resolved endpoint yields `not_configured`.

- [ ] **Step 4: Integrate capabilities and observations**

In `Detect`, assign a copy of `CapabilitiesFor(tmpl.ID)` for every service. For supported metrics:

1. emit `disabled` when explicitly disabled;
2. emit `not_configured` when no explicit or discovered endpoint exists;
3. otherwise call `CollectTelemetryWithRunner`, assign its report-safe telemetry, and pass each original diagnostic to `Deps.Logf` when non-nil;
4. call `svc.PopulateLegacyStats()` after telemetry is assigned;
5. calculate `svc.Status` exactly as before, without consulting telemetry.

Add small constructors for paired `disabled` and `not_configured` observations so both supported metrics receive the same state while sing-box still omits its unsupported online observation.

Keep the exported `Build` signature stable by adding an unexported helper:

```go
func build(ctx context.Context, cfg *config.Config, version string, runner detect.Runner, logf func(string, ...any)) (*report.Report, error)
```

`Build` calls `build` with `nil` for deterministic library use. `Run` calls it with `logger.Debugf`, so full errors are available locally only when debug logging is enabled. Add a `build` test with a captured callback and a controlled failing statistics endpoint. Assert the callback receives the original failure while JSON marshaling of the returned report contains only the safe error code.

- [ ] **Step 5: Run package tests**

Run: `gofmt -w internal/config/config.go internal/config/config_test.go internal/detect/detect.go internal/detect/detect_test.go internal/agent/run.go internal/agent/run_test.go && go test ./internal/config ./internal/detect ./internal/agent`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/detect/detect.go internal/detect/detect_test.go internal/agent/run.go internal/agent/run_test.go
git commit -m "feat: report telemetry availability from detection"
```

---

### Task 5: Discover VLESS without creating a service

**Files:**
- Modify: `internal/detect/systemd.go`
- Modify: `internal/detect/systemd_test.go`
- Create: `internal/detect/protocols.go`
- Create: `internal/detect/protocols_test.go`
- Modify: `internal/detect/detect.go`
- Modify: `internal/detect/detect_test.go`

**Interfaces:**
- Produces: `UnitInfo.ExecArgs []string`.
- Produces: `func DiscoverProtocols(serviceType string, execArgs, fallbackPaths []string) (*report.ProtocolInfo, error)`; the error is local-only.
- Consumes: existing `Template.StatsConfigPaths` as safe fallback service-config locations for Xray and sing-box.

- [ ] **Step 1: Write failing `ExecStart` parsing test**

Extend `TestShowUnit`:

```go
wantArgs := []string{"/usr/local/bin/hysteria", "server", "-c", "/etc/hysteria/config.yaml"}
if !reflect.DeepEqual(u.ExecArgs, wantArgs) {
	t.Fatalf("ExecArgs = %#v, want %#v", u.ExecArgs, wantArgs)
}
```

- [ ] **Step 2: Verify the parser test fails**

Run: `go test ./internal/detect -run TestShowUnit`

Expected: FAIL because `UnitInfo` retains only the executable path.

- [ ] **Step 3: Retain systemd arguments**

Change the internal parser to return both the `path=` value and fields from `argv[]=`. Keep `ExecStart` unchanged for version execution and fill the new `ExecArgs` field for configuration-path discovery.

- [ ] **Step 4: Write failing VLESS discovery tests**

Create table tests with temporary files for:

```go
func TestDiscoverProtocolsXrayVLESS(t *testing.T) {
	path := writeProtocolConfig(t, `{"inbounds":[{"protocol":"vless","settings":{"clients":[{"id":"secret-uuid"}]}},{"protocol":"vless"}]}`)
	got, err := DiscoverProtocols("xray", []string{"xray", "run", "-config", path}, nil)
	if err != nil { t.Fatal(err) }
	if got.State != "ok" || !reflect.DeepEqual(got.Items, []string{"vless"}) {
		t.Fatalf("protocols = %+v", got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "secret-uuid") { t.Fatalf("secret leaked: %s", b) }
}

func TestDiscoverProtocolsSingBoxVLESS(t *testing.T) {
	path := writeProtocolConfig(t, `{"inbounds":[{"type":"vless","users":[{"uuid":"private"}]}]}`)
	got, err := DiscoverProtocols("sing-box", []string{"sing-box", "run", "-c", path}, nil)
	if err != nil { t.Fatal(err) }
	if got.State != "ok" || !reflect.DeepEqual(got.Items, []string{"vless"}) {
		t.Fatalf("protocols = %+v", got)
	}
}
```

Also test: valid config with no VLESS returns `ok` and empty items; duplicates are deduplicated; an explicit unreadable path returns `error`; an unimplemented service returns `unknown`; no sensitive fixture value appears in serialized output.

- [ ] **Step 5: Verify discovery tests fail**

Run: `go test ./internal/detect -run TestDiscoverProtocols`

Expected: FAIL because `DiscoverProtocols` does not exist.

- [ ] **Step 6: Implement VLESS-only discovery**

Recognize Xray `-config`, `-c`, and `-confdir`, plus sing-box `-c`, `--config`, `-C`, and `--config-directory`, including `--flag=value` forms. Prefer explicit `ExecArgs`; use fallback paths only when no explicit path was found. Read regular files and sorted `.json`, `.yaml`, or `.yml` directory entries with the existing YAML v3 parser.

Decode only minimal inbound shapes:

```go
type protocolConfig struct {
	Inbounds []map[string]any `yaml:"inbounds"`
}
```

For Xray, inspect only `protocol`; for sing-box, inspect only `type`; append only the literal normalized value `vless`. Never copy any other configuration value into the result or error fields.

- [ ] **Step 7: Attach protocol information during detection**

For Xray and sing-box services, call `DiscoverProtocols(tmpl.ID, info.ExecArgs, tmpl.StatsConfigPaths)` and assign the returned safe result to `svc.Protocols`. Pass its original error to `Deps.Logf` when non-nil. Other built-in services receive `state=unknown` only if a protocol object is needed; omitting the optional field remains valid and is normalized to unknown by the Panel.

Add a detector integration test confirming an Xray service remains one service whose `Protocols.Items` contains `vless`.

- [ ] **Step 8: Run detector tests**

Run: `gofmt -w internal/detect/systemd.go internal/detect/systemd_test.go internal/detect/protocols.go internal/detect/protocols_test.go internal/detect/detect.go internal/detect/detect_test.go && go test ./internal/detect ./internal/agent`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/detect/systemd.go internal/detect/systemd_test.go internal/detect/protocols.go internal/detect/protocols_test.go internal/detect/detect.go internal/detect/detect_test.go
git commit -m "feat: discover VLESS protocol tags"
```

---

### Task 6: Add the Panel compatibility and display-state layer

**Files:**
- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Modify: `web/src/lib/api.ts`
- Create: `web/src/lib/serviceTelemetry.ts`
- Create: `web/src/lib/serviceTelemetry.test.ts`

**Interfaces:**
- Produces: additive TypeScript interfaces matching Task 1.
- Produces: `normalizeTraffic(service: Service): NormalizedTraffic`.
- Produces: `normalizeOnlineClients(service: Service): NormalizedCount`.
- Produces: `metricLabel(metric: NormalizedTraffic | NormalizedCount): string` and `protocolLabels(service: Service): string[]`.

- [ ] **Step 1: Install and wire the existing-stack test runner**

Run: `cd web && npm install --save-dev vitest@2.1.9`

Add scripts:

```json
"test": "vitest run",
"verify": "npm run test && npm run typecheck && npm run build && npm run check:bundle"
```

Vitest is a development dependency and must not appear in production chunks.

- [ ] **Step 2: Add additive wire types**

In `web/src/lib/api.ts`, add string-union types matching the Go contract and extend `Service` with optional `protocols`, `capabilities`, and `telemetry`. Keep legacy `stats` unchanged.

- [ ] **Step 3: Write failing normalization tests**

```ts
import { describe, expect, it } from "vitest";
import { metricLabel, normalizeOnlineClients, normalizeTraffic, protocolLabels } from "./serviceTelemetry";

describe("service telemetry compatibility", () => {
  it("preserves a successful zero", () => {
    const service = fixture({
      capabilities: { traffic: { support: "supported" }, online_clients: { support: "supported" } },
      telemetry: { traffic: { state: "ok", tx_bytes: 0, rx_bytes: 0 } },
    });
    expect(normalizeTraffic(service)).toMatchObject({ kind: "ok", tx: 0, rx: 0 });
  });

  it("labels native unavailability separately", () => {
    const service = fixture({ capabilities: {
      traffic: { support: "unsupported", reason_code: "native_api_unavailable" },
      online_clients: { support: "unsupported", reason_code: "native_api_unavailable" },
    }});
    expect(metricLabel(normalizeTraffic(service))).toBe("内核不支持");
  });

  it("does not trust legacy online_clients zero", () => {
    const service = fixture({ stats: { tx: 7, rx: 9, online_clients: 0 } });
    expect(normalizeTraffic(service).kind).toBe("ok");
    expect(normalizeOnlineClients(service).kind).toBe("unknown");
  });

  it("returns the VLESS label", () => {
    const service = fixture({ protocols: { state: "ok", items: ["vless"], source: "config" } });
    expect(protocolLabels(service)).toEqual(["VLESS"]);
  });
});
```

The local `fixture` returns a minimally valid `Service` and allows an additive `Partial<Service>` override.

- [ ] **Step 4: Verify failure**

Run: `cd web && npm test -- src/lib/serviceTelemetry.test.ts`

Expected: FAIL because the helper module does not exist.

- [ ] **Step 5: Implement normalization and labels**

Use a discriminated union:

```ts
export type NormalizedTraffic =
  | { kind: "ok"; tx: number; rx: number; legacy: boolean }
  | { kind: "unsupported" | "not_implemented" | "not_configured" | "disabled" | "error" | "unknown"; errorCode?: string };

export type NormalizedCount =
  | { kind: "ok"; value: number; legacy: boolean }
  | { kind: "unsupported" | "not_implemented" | "not_configured" | "disabled" | "error" | "unknown"; errorCode?: string };
```

Rules:

- New telemetry wins over legacy `stats`.
- New capability `unsupported/native_api_unavailable` becomes `unsupported`.
- `unsupported/collector_not_implemented` becomes `not_implemented`.
- A legacy `stats` object yields successful traffic but unknown online clients.
- No capability and no legacy data becomes `unknown`.
- Unknown enum values become `unknown`.
- `protocolLabels` returns uppercase `VLESS` only for `protocols.state=ok` and the exact `vless` item.

Map normalized states to the approved Chinese labels in one function so cards and empty states cannot drift.

- [ ] **Step 6: Run frontend unit and type tests**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/package.json web/package-lock.json web/src/lib/api.ts web/src/lib/serviceTelemetry.ts web/src/lib/serviceTelemetry.test.ts
git commit -m "feat: normalize service telemetry in panel"
```

---

### Task 7: Render protocol tags and explicit metric states

**Files:**
- Create: `web/src/components/ServiceCard.tsx`
- Create: `web/src/components/ServiceCard.test.tsx`
- Modify: `web/src/pages/NodeDetail.tsx`

**Interfaces:**
- Consumes: `Service`, `normalizeTraffic`, `normalizeOnlineClients`, `metricLabel`, and `protocolLabels`.
- Produces: `ServiceCard({ service }: { service: Service })`.

- [ ] **Step 1: Write failing server-rendered component tests**

```tsx
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import ServiceCard from "./ServiceCard";

it("renders VLESS on the host service without a duplicate service", () => {
  const html = renderToStaticMarkup(<ServiceCard service={xrayFixture({
    protocols: { state: "ok", items: ["vless"], source: "config" },
  })} />);
  expect(html).toContain("xray");
  expect(html).toContain("VLESS");
  expect((html.match(/VLESS/g) ?? []).length).toBe(1);
});

it("renders explicit unavailable and zero values", () => {
  const html = renderToStaticMarkup(<ServiceCard service={anyTLSFixture()} />);
  expect(html).toContain("内核不支持");
  const zero = renderToStaticMarkup(<ServiceCard service={zeroHysteriaFixture()} />);
  expect(zero).toContain("↓0 B ↑0 B");
  expect(zero).toContain("0");
});
```

Keep fixture builders in the test file so tests cannot accidentally use production defaults.

- [ ] **Step 2: Verify failure**

Run: `cd web && npm test -- src/components/ServiceCard.test.tsx`

Expected: FAIL because the component does not exist.

- [ ] **Step 3: Extract and implement `ServiceCard`**

Move the current service-card markup out of `NodeDetail.tsx`. Render the implementation name as the main title and protocol tags next to it. Show:

- successful traffic as `↓{rx} ↑{tx}`, including zero;
- successful online clients as an integer, including zero;
- all other states through the centralized approved label;
- safe `errorCode` text in `title`, never a raw Agent error.

Do not create one card per protocol. Do not feed protocol tags into traffic aggregation.

- [ ] **Step 4: Replace inline cards on the node page**

Map each detected non-generic service to one `ServiceCard`. Keep the current key stable with service type, unit, and index. The service count and distribution remain instance-based.

- [ ] **Step 5: Run frontend verification**

Run: `cd web && npm test && npm run typecheck && npm run build && npm run check:bundle`

Expected: PASS; every emitted JavaScript chunk remains at or below 512,000 bytes.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/ServiceCard.tsx web/src/components/ServiceCard.test.tsx web/src/pages/NodeDetail.tsx
git commit -m "feat: show service capabilities and VLESS tags"
```

---

### Task 8: Make traffic trends capability-aware

**Files:**
- Create: `web/src/lib/traffic.ts`
- Create: `web/src/lib/traffic.test.ts`
- Modify: `web/src/pages/NodeDetail.tsx`

**Interfaces:**
- Consumes: `Metric`, `Service`, and `normalizeTraffic`.
- Produces: `aggregateTraffic(metrics: Metric[]): TrafficSummary`.
- Produces: chart points with `tx`, `rx`, `successful`, `eligible`, and `partial`.

- [ ] **Step 1: Write failing aggregation tests**

```ts
describe("aggregateTraffic", () => {
  it("keeps an all-zero successful point", () => {
    const summary = aggregateTraffic([metric(1, [serviceWithTraffic(0, 0)])]);
    expect(summary.points).toEqual([{ ts: 1, tx: 0, rx: 0, successful: 1, eligible: 1, partial: false }]);
    expect(summary.emptyState).toBeNull();
  });

  it("uses null for collection gaps", () => {
    const summary = aggregateTraffic([metric(1, [serviceWithTrafficError("timeout")])]);
    expect(summary.points[0]).toMatchObject({ tx: null, rx: null, successful: 0, eligible: 1 });
    expect(summary.emptyState).toBe("no_valid_data");
  });

  it("marks partial aggregates", () => {
    const summary = aggregateTraffic([metric(1, [serviceWithTraffic(7, 9), serviceWithTrafficError("timeout")])]);
    expect(summary.points[0]).toMatchObject({ tx: 7, rx: 9, successful: 1, eligible: 2, partial: true });
  });

  it("distinguishes unsupported from unknown", () => {
    expect(aggregateTraffic([metric(1, [unsupportedService()])]).emptyState).toBe("all_unsupported");
    expect(aggregateTraffic([metric(1, [legacyServiceWithoutStats()])]).emptyState).toBe("capabilities_unknown");
  });
});
```

- [ ] **Step 2: Verify failure**

Run: `cd web && npm test -- src/lib/traffic.test.ts`

Expected: FAIL because `aggregateTraffic` does not exist.

- [ ] **Step 3: Implement deterministic aggregation**

Parse each `services_json` independently. For every timestamp:

- eligible = services normalized as supported (`ok`, `not_configured`, `disabled`, or `error`);
- successful = eligible services whose traffic is `ok`;
- sum only successful services;
- use numeric zero when success is real;
- use `null` when eligible services exist but none succeeded;
- exclude unsupported and unknown services from totals;
- set `partial` when `0 < successful < eligible`.

Malformed JSON contributes no fabricated values and is treated as unknown coverage.

- [ ] **Step 4: Update the chart and empty states**

Replace the current inline map/filter. Preserve null chart gaps. Add the approved empty strings:

```ts
const EMPTY_LABELS = {
  all_unsupported: "当前服务不提供流量指标",
  capabilities_unknown: "能力未知，请升级探针",
  no_valid_data: "暂无有效采集数据",
} as const;
```

Use a custom tooltip that shows `successful/eligible 个可采集服务` when `partial` is true. A zero point must render normally and must not be removed by a truthiness filter.

- [ ] **Step 5: Run frontend verification**

Run: `cd web && npm test && npm run verify`

Expected: PASS; bundle budget remains green.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/traffic.ts web/src/lib/traffic.test.ts web/src/pages/NodeDetail.tsx
git commit -m "feat: aggregate traffic with capability coverage"
```

---

### Task 9: Verify API compatibility and the privacy boundary

**Files:**
- Modify: `internal/panel/api/report_test.go`
- Modify: `internal/agent/integration_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: the full additive report contract.
- Verifies: existing report ingestion stores nested JSON unchanged and accepts legacy JSON.

- [ ] **Step 1: Write an extended report persistence test**

POST a schema-version-1 report containing:

```json
{
  "type": "xray",
  "protocols": {"state":"ok","items":["vless"],"source":"config"},
  "capabilities": {
    "traffic":{"support":"supported","source":"native_api"},
    "online_clients":{"support":"supported","source":"native_api"}
  },
  "telemetry": {
    "traffic":{"state":"ok","tx_bytes":0,"rx_bytes":0},
    "online_clients":{"state":"ok","value":0}
  }
}
```

Read `last_services_json` and assert VLESS and all three zero values remain present. Keep the existing legacy report test unchanged to prove backward compatibility.

- [ ] **Step 2: Verify the focused API test fails before any needed fixes**

Run: `go test ./internal/panel/api -run 'TestHandleExtendedReport|TestHandleReport'`

Expected: The new test either passes through existing JSON persistence or exposes a contract mismatch. If it passes, no production API change is required; retain the test as the compatibility gate.

- [ ] **Step 3: Extend the Agent integration assertion**

Make the fake Panel capture a report for an Xray fixture with VLESS configuration. Assert:

- exactly one Xray service exists;
- its protocol items contain only `vless`;
- capabilities and telemetry are present;
- legacy `stats` remains present after successful collection;
- the serialized body does not contain fixture UUID, secret, endpoint authorization header, or full config text.

- [ ] **Step 4: Update documentation**

Document:

- the distinction between service implementations and protocol tags;
- VLESS as an Xray/sing-box tag with service-instance totals;
- each capability and observation state and its Panel label;
- `[stats.services.<type>] enabled = false`;
- legacy-Agent behavior and the recommendation to upgrade Agents for precise capability labels.

Do not list VLESS as a ninth standalone detected daemon.

- [ ] **Step 5: Run integration and documentation-adjacent gates**

Run: `gofmt -w internal/panel/api/report_test.go internal/agent/integration_test.go && go test ./internal/panel/api ./internal/agent && bash -n install.sh install-panel.sh tests/*.sh`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/panel/api/report_test.go internal/agent/integration_test.go README.md
git commit -m "docs: explain service telemetry capabilities"
```

---

### Task 10: Run full verification and measure Agent size impact

**Files:**
- Verify only; do not create generated artifacts in the repository.

**Interfaces:**
- Verifies all acceptance criteria and release gates.

- [ ] **Step 1: Run all backend quality gates**

Run:

```bash
go test ./...
go vet ./...
go build ./...
```

Expected: all commands exit 0.

- [ ] **Step 2: Run all frontend gates**

Run: `cd web && npm ci && npm run verify`

Expected: Vitest, TypeScript, Vite build, and the 512,000-byte chunk gate all pass.

- [ ] **Step 3: Run release and installer contracts**

Run:

```bash
bash -n install.sh install-panel.sh tests/*.sh
bash tests/release-contract.sh
bash tests/ci-contract.sh
NET_PROBE_INSTALLER_SMOKE=1 bash tests/installers-smoke.sh
```

Expected: all gates exit 0. Use the same privileges as CI for the installer smoke test if its system paths require them.

- [ ] **Step 4: Measure the stripped linux/amd64 Agent**

Build the feature branch with the release flags:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -buildid=" -o /tmp/net-probe-capabilities ./cmd/net-probe
wc -c /tmp/net-probe-capabilities
xz -9e -kf /tmp/net-probe-capabilities
wc -c /tmp/net-probe-capabilities.xz
```

Build commit `8e27981291139d6ec7b241634aa30176dca4d785` in a temporary detached worktree with the identical commands and compare raw and xz sizes. Record both deltas in the PR description. Confirm `go.mod` has no new runtime dependency.

- [ ] **Step 5: Review the acceptance checklist**

Confirm from fresh evidence:

- AnyTLS explicitly reports native unavailability.
- successful zeros survive Agent JSON, Panel persistence, normalization, and chart aggregation;
- old reports remain accepted;
- VLESS appears once on its hosting service;
- sensitive fixture values never appear in reports;
- telemetry errors do not alter service health;
- no SQL migration or Agent dependency was added.

- [ ] **Step 6: Commit only verification-driven fixes**

If verification required source changes, repeat the failing focused test, apply one fix, rerun the full gate, and commit the scoped fix. If no changes were needed, do not create an empty verification commit.
