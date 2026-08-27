# Service Capabilities and VLESS Protocol Tag Design

**Date:** 2026-08-27

**Status:** Approved for implementation planning

**Scope:** Agent report contract, service statistics collectors, VLESS protocol discovery, Panel service cards and traffic trends

## 1. Context

The Agent detects several proxy service implementations:

- Hysteria2
- Xray
- V2Ray
- sing-box
- Shadowsocks
- Trojan
- TUIC
- AnyTLS

These implementations do not expose the same observability features. Hysteria2 and Xray can expose traffic and online-client statistics when their statistics APIs are enabled. The current sing-box collector exposes traffic but not online clients. AnyTLS does not provide a native source for either metric. Other service types do not yet have dedicated collectors in this project.

The current report contract cannot represent this difference. `service.stats` is either absent or contains non-null integer fields. As a result, the Panel cannot distinguish:

- the service implementation does not expose the metric;
- the Agent has not implemented a collector yet;
- the service supports the metric but its API is not configured;
- collection failed;
- collection succeeded and the true value is zero;
- the report came from an older Agent that does not declare capabilities.

The existing service list also mixes service implementations with protocols. VLESS is a protocol normally hosted by Xray or sing-box, not a standalone service runtime. Treating it as another service would duplicate service cards and double-count instance-level metrics.

## 2. Decisions

The agreed product decisions are:

1. Unsupported metrics are shown explicitly rather than hidden or rendered as an unexplained dash.
2. The Panel remains compatible with v0.1.0 Agents.
3. The Agent reports native, verifiable data only. It does not estimate protocol traffic from host network counters, socket counts, eBPF, or cgroups.
4. Statistics remain service-instance aggregates. This phase does not introduce per-inbound, per-protocol, or per-user statistics.
5. VLESS is represented as a protocol tag on an Xray or sing-box service instance.
6. Service health, metric capability, and metric collection health are independent dimensions.

## 3. Goals

- Make the report self-describing so the Panel does not maintain a second capability matrix.
- Preserve a real zero value and distinguish it from missing or failed collection.
- Explain unavailable data with stable machine-readable reason codes and clear UI labels.
- Add best-effort VLESS discovery without uploading configuration secrets.
- Preserve old-Agent/new-Panel and new-Agent/old-Panel compatibility during migration.
- Reuse the existing `services_json` persistence path and avoid a database migration.
- Avoid new heavyweight Agent dependencies.

## 4. Non-goals

- Per-inbound, per-protocol, or per-user traffic attribution.
- Estimating application metrics from sockets, host NIC counters, firewall counters, cgroups, or eBPF.
- Parsing and displaying UUIDs, passwords, domains, transport paths, or complete service configurations.
- Adding protocol discovery for every supported service in this phase. Only VLESS discovery is required initially.
- Changing service availability alerts because a statistics endpoint is unavailable.
- Normalizing service telemetry into new SQL tables.
- Removing the legacy `stats` field in schema version 1.

## 5. Terminology

### 5.1 Service implementation

A running daemon or service instance discovered through systemd and process metadata, such as `xray`, `sing-box`, or `anytls`. It remains the unit of service cards, status, and statistics aggregation.

### 5.2 Protocol tag

A normalized protocol name discovered from the effective service configuration. VLESS is represented as `vless` on the hosting service instance. A protocol tag does not create another service or another metric stream.

### 5.3 Capability

Whether the Agent can obtain a metric from a trustworthy native source for this service implementation.

### 5.4 Telemetry observation

The result of attempting to collect one supported metric during the current Agent run.

## 6. Architecture

The Agent remains responsible for discovery and collection:

```text
systemd/process discovery
        |
        v
service template + collector descriptor
        |                         |
        |                         +--> protocol discovery (VLESS tag)
        v
per-metric capability declaration
        |
        v
native metric collection
        |
        v
report: service + capabilities + telemetry + legacy stats
        |
        v
Panel persistence in existing services_json
        |
        v
capability-aware service cards and trend aggregation
```

Each statistics collector owns its capability descriptor. The Panel renders reported capabilities and never infers support merely from `service.type`.

Service templates continue to identify processes and configuration locations. Collector code, rather than YAML presentation metadata, defines whether a metric has a supported native collection path. This keeps capability declarations next to the code that must satisfy them.

## 7. Report Contract

The change is additive and retains `schema_version: "1"`. Three optional fields are added to each service:

- `protocols`: protocol discovery result;
- `capabilities`: per-metric support declarations;
- `telemetry`: per-metric collection results.

### 7.1 Protocol discovery

```json
{
  "type": "xray",
  "protocols": {
    "state": "ok",
    "items": ["vless"],
    "source": "config"
  }
}
```

`protocols.state` values:

| State | Meaning |
|---|---|
| `ok` | Configuration was read successfully. `items` may legitimately be empty. |
| `unknown` | The Agent or service type does not implement protocol discovery. |
| `error` | Discovery was attempted but the effective configuration could not be read or parsed. |

`items` is a sorted, deduplicated list of lowercase identifiers. The initial recognized value is `vless`.

### 7.2 Capabilities

```json
{
  "capabilities": {
    "traffic": {
      "support": "supported",
      "source": "native_api"
    },
    "online_clients": {
      "support": "unsupported",
      "reason_code": "native_api_unavailable"
    }
  }
}
```

`support` values:

| Value | Meaning |
|---|---|
| `supported` | A collector exists and uses a native, trustworthy source. |
| `unsupported` | The metric cannot currently be collected; `reason_code` explains why. |
| `unknown` | The Agent cannot determine the capability. |

Capability reason codes:

| Code | Meaning | Panel label |
|---|---|---|
| `native_api_unavailable` | The implementation exposes no suitable native source. | 内核不支持 |
| `collector_not_implemented` | A collector has not been implemented or verified. | 探针暂未支持 |
| `agent_too_old` | Synthesized by the Panel for a legacy report without capability fields. | 能力未知，请升级探针 |

The Panel must treat unknown reason codes as `unknown` rather than failing report rendering.

### 7.3 Telemetry

Traffic and online clients are separate observations:

```json
{
  "telemetry": {
    "traffic": {
      "state": "ok",
      "tx_bytes": 0,
      "rx_bytes": 0
    },
    "online_clients": {
      "state": "ok",
      "value": 0
    }
  }
}
```

Observation states:

| State | Meaning | Values |
|---|---|---|
| `ok` | Collection succeeded. | Required, including numeric zero. |
| `not_configured` | The native statistics interface is not enabled or has no endpoint. | Omitted. |
| `disabled` | Collection was explicitly disabled by Agent configuration. | Omitted. |
| `error` | Collection was attempted and failed. | Omitted; `error_code` required. |

Collection error codes are stable and non-sensitive:

- `timeout`
- `unauthorized`
- `connection_failed`
- `invalid_response`
- `command_failed`
- `config_unreadable`
- `unknown`

Raw endpoint URLs, authorization headers, secrets, command output, and configuration fragments must not be uploaded. Detailed diagnostics remain in local Agent logs.

An unsupported capability has no telemetry observation. A supported capability always has an observation for the current run, including `not_configured`, `disabled`, or `error`.

### 7.4 Complete examples

AnyTLS with no native metric source:

```json
{
  "type": "anytls",
  "protocols": {"state": "unknown", "items": []},
  "capabilities": {
    "traffic": {
      "support": "unsupported",
      "reason_code": "native_api_unavailable"
    },
    "online_clients": {
      "support": "unsupported",
      "reason_code": "native_api_unavailable"
    }
  },
  "telemetry": {}
}
```

Xray hosting VLESS with a statistics endpoint that is not configured:

```json
{
  "type": "xray",
  "protocols": {
    "state": "ok",
    "items": ["vless"],
    "source": "config"
  },
  "capabilities": {
    "traffic": {"support": "supported", "source": "native_api"},
    "online_clients": {"support": "supported", "source": "native_api"}
  },
  "telemetry": {
    "traffic": {"state": "not_configured"},
    "online_clients": {"state": "not_configured"}
  }
}
```

VLESS does not receive its own telemetry object. Xray telemetry represents the entire Xray service instance.

## 8. Initial Capability Matrix

The initial matrix describes verified project collectors, not every theoretical capability of every upstream implementation:

| Service implementation | Traffic | Online clients |
|---|---|---|
| Hysteria2 | `supported`; native API must be enabled | `supported`; native API must be enabled |
| Xray | `supported`; Stats API must be enabled | `supported`; corresponding API/command must be available |
| sing-box | `supported`; Clash API must be enabled | `unsupported/native_api_unavailable` |
| AnyTLS | `unsupported/native_api_unavailable` | `unsupported/native_api_unavailable` |
| V2Ray | `unsupported/collector_not_implemented` | `unsupported/collector_not_implemented` |
| Shadowsocks | `unsupported/collector_not_implemented` | `unsupported/collector_not_implemented` |
| Trojan | `unsupported/collector_not_implemented` | `unsupported/collector_not_implemented` |
| TUIC | `unsupported/collector_not_implemented` | `unsupported/collector_not_implemented` |
| Unrecognized/generic | `unknown` | `unknown` |

If a later collector verifies a native source, only that collector descriptor and its tests change. The Panel does not require a capability-matrix release.

## 9. VLESS Discovery

### 9.1 Xray

The Agent identifies effective configuration paths from the service `ExecStart` arguments when possible, then falls back to known standard paths. It scans configured inbounds and emits `vless` when an inbound protocol is VLESS.

Configuration directories may contain multiple fragments. Protocol results are merged, normalized, deduplicated, and sorted. Failure to parse one effective configuration set produces `protocols.state=error`; it does not change service health.

### 9.2 sing-box

The Agent follows the same effective-path-first strategy and emits `vless` when an inbound type is VLESS.

### 9.3 Privacy and failure isolation

Only normalized protocol identifiers leave the host. The protocol parser must not retain or serialize user identifiers, credentials, TLS material, domains, transport paths, or arbitrary configuration values.

Protocol discovery is best effort. Missing permissions, unsupported syntax, or malformed configuration never prevents the service or host report from being uploaded.

## 10. Backward Compatibility

### 10.1 New Panel receiving an old Agent report

- If legacy `stats` exists, traffic values may be displayed as legacy observed data.
- Legacy `online_clients` is not treated as a reliable capability signal because old sing-box reports serialize an indistinguishable zero.
- Missing capability fields are rendered as `unknown/agent_too_old`.
- Missing protocol fields mean protocol discovery is unknown, not that the service has no protocols.

### 10.2 Old Panel receiving a new Agent report

The new fields are additive and ignored by old JSON decoders. During schema version 1, the Agent continues to populate legacy `stats` from successful new telemetry observations so existing Panels retain their current behavior.

Legacy `stats` is deprecated but is not removed until a future schema version explicitly drops it.

## 11. Panel Behavior

### 11.1 Service cards

The title remains the service implementation. Protocols appear as compact tags:

```text
Xray  [VLESS]
```

If protocol discovery is unknown or fails, the Panel does not render a misleading “no protocols” message. An optional detail hint may say “协议未识别”.

Metric display rules:

| Report state | Display |
|---|---|
| `ok` with zero | `0 B` or `0 个` |
| `unsupported/native_api_unavailable` | 内核不支持 |
| `unsupported/collector_not_implemented` | 探针暂未支持 |
| `not_configured` | 需启用统计接口 |
| `disabled` | 已禁用 |
| `error` | 采集失败, with a safe reason-code hint |
| `unknown` | 能力未知，请升级探针 |

Statistics state does not override `service.status`. A running service remains healthy when optional telemetry is unsupported, disabled, not configured, or temporarily failing.

### 11.2 Traffic trends

For each timestamp:

1. Include only services whose traffic observation is `state=ok`.
2. Preserve successful zero values as valid chart points.
3. Exclude unsupported services from the eligible count.
4. Treat supported but unsuccessful observations as missing coverage, not zero.
5. Sum successful service-instance traffic and attach `successful/eligible` coverage metadata.
6. When no service succeeded but at least one was eligible, emit a chart gap rather than a zero.

The tooltip displays coverage when incomplete, for example `2/4 个可采集服务`. A partial aggregate is allowed but must be visibly identified.

Empty states are distinct:

- all services unsupported: `当前服务不提供流量指标`;
- capabilities unknown because Agents are old: `能力未知，请升级探针`;
- supported services exist but no successful observations: `暂无有效采集数据`.

The current filter that removes points when both values are zero must be removed.

## 12. Persistence and API

The Panel continues storing the complete service array in `nodes.last_services_json` and `metrics.services_json`. The new nested fields flow through the existing JSON storage without a SQL schema migration.

The report ingestion API accepts both legacy and extended schema-version-1 reports. Unknown capability, telemetry, protocol, state, and reason-code values must degrade to an unknown presentation instead of rejecting the complete report.

## 13. Error Handling

- One metric collection failure does not erase successful metrics from the same service.
- A statistics error does not abort detection or report upload.
- A protocol-discovery error does not abort service detection or report upload.
- Uploaded errors use bounded stable codes, not raw upstream error text.
- Full errors are logged locally at the configured Agent log level.
- Unsupported, disabled, and not-configured states do not create service-health alerts.

Telemetry-specific alerting is outside this phase and may be added later using repeated state transitions rather than a single failed sample.

## 14. Testing Strategy

### 14.1 Contract tests

- Successful zero traffic and zero online-client values remain present in JSON.
- `ok` requires values; non-`ok` states omit values.
- Supported capabilities have one telemetry observation per Agent run.
- Unsupported capabilities have a stable reason code and no fabricated observation.
- New reports still contain legacy `stats` when a successful observation can populate it.
- Old schema-version-1 reports remain accepted.

### 14.2 Collector tests

- Hysteria2 and Xray declare both metrics supported.
- sing-box declares traffic supported and online clients unavailable.
- AnyTLS declares both metrics natively unavailable.
- Unimplemented service collectors use `collector_not_implemented`.
- Timeout, authorization, connection, command, and decode failures map to safe error codes.

### 14.3 Protocol discovery tests

- Xray configuration containing a VLESS inbound yields `items=["vless"]`.
- sing-box configuration containing a VLESS inbound yields `items=["vless"]`.
- Duplicate VLESS inbounds produce one sorted tag.
- A valid configuration without VLESS yields `state=ok` with an empty list.
- Missing or malformed configuration produces the expected non-fatal state.
- Serialized reports never contain fixture UUIDs, passwords, domains, or complete configuration fragments.

### 14.4 Panel tests

- AnyTLS shows `内核不支持`, not `暂无数据`.
- Legacy reports show capability unknown without crashing.
- A successful zero renders as zero and remains in trend data.
- Failed collection creates a chart gap rather than a zero.
- Partial aggregates show coverage.
- Xray and sing-box show a VLESS tag without creating a second service card.
- Service health remains independent from telemetry state.

## 15. Rollout

1. Add the additive report types and compatibility serialization.
2. Refactor existing collectors to declare per-metric capabilities and observations.
3. Add descriptors for services without collectors.
4. Add safe VLESS discovery for Xray and sing-box.
5. Update Panel types, service cards, empty states, and trend aggregation.
6. Update README examples and the supported-service explanation.
7. Run Agent, Panel, installer, contract, and release gates before integration.

No new heavyweight Agent dependency is permitted for this feature. Existing parsers and the standard library should be reused. Release verification records the Agent binary-size delta so this work does not silently undermine the separate size-reduction objective.

## 16. Acceptance Criteria

- The Panel distinguishes unsupported, not implemented, not configured, disabled, failed, unknown, and successful-zero states.
- AnyTLS traffic and online-client rows explicitly say the native capability is unavailable.
- VLESS appears as a tag on Xray or sing-box, never as a duplicate service.
- Traffic and online-client data remain service-instance totals.
- Old Agents continue reporting successfully to the new Panel.
- New Agents remain consumable by old Panels through legacy `stats`.
- No sensitive configuration content is included in reports or API errors.
- Existing database files upgrade without migration.
- Service health and alerts are unchanged by optional telemetry availability.
- All new behavior is covered by contract, collector, protocol-discovery, and Panel tests.
