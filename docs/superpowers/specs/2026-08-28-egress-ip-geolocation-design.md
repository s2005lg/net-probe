# Agent Egress IP and Panel Geolocation Design

## Status

Approved in conversation; pending review of this written specification.

## Context

The Agent currently reports the first non-loopback address found on a local
network interface as `host.ipv4` or `host.ipv6`. That value is not necessarily
the address used to reach the public Internet when the host is behind NAT or
has multiple routes.

The Panel already persists `ip_location`, `ip_country`, `ip_region`, and
`ip_city`, but its geolocation job only runs at Panel startup and then every
configured refresh interval (12 hours by default). A Panel started before its
first Agent report therefore leaves the new node's location empty until the
next periodic run. The current provider is also accessed over plaintext HTTP.

The node table and card use "IP出口地址" inconsistently: the value is a
geographic location, while the adjacent `IP` value is only a local interface
address.

## Goals

- Detect the public IPv4 and IPv6 addresses used by an Agent host without
  assuming that a local interface address is publicly routable.
- Report the detected addresses to the Panel using an additive,
  backward-compatible report contract.
- Resolve and persist country, region, and city in the Panel as soon as a new
  egress address is reported.
- Keep Agent collection and reporting functional when public-IP or
  geolocation providers are unavailable.
- Avoid querying public-IP services on every one-minute Agent run.
- Display the egress IP and its geographic location with unambiguous labels.

## Non-goals

- GPS-level or street-level location accuracy.
- VPN, proxy, Tor, ASN, ISP, or threat classification.
- Replacing local interface addresses; `host.ipv4` and `host.ipv6` remain
  available for diagnostics.
- Uploading provider responses, request metadata, or credentials.
- Treating geolocation failure as a node-health or service-health failure.

## Chosen architecture

The Agent discovers public egress addresses. The Panel receives those
addresses, performs geolocation, and owns the persisted geographic data.

This keeps IP discovery correct for NAT and reverse-proxy deployments while
centralizing provider selection, caching, rate limiting, and geographic data
formatting in the Panel.

Panel inference from the Agent request's remote address is not the primary
mechanism because a reverse proxy or CDN can replace that address. It may be
used only as a future diagnostic or fallback and is outside this change.

## Report contract

Add optional fields to `report.Host`:

```go
EgressIPv4 string `json:"egress_ipv4,omitempty"`
EgressIPv6 string `json:"egress_ipv6,omitempty"`
```

The fields contain canonical textual IP addresses validated with the Go
standard library. They never contain a host name, prefix length, port, or raw
provider response.

The change is additive, so `schema_version` remains `1`:

- a new Panel accepts reports from old Agents without these fields;
- an old Panel ignores the additional JSON fields;
- an empty field means that the address family was not discovered during this
  or a previous successful refresh.

The effective display and lookup address is selected in this order:

1. `host.egress_ipv4`
2. `host.egress_ipv6`
3. `host.ipv4` for an old Agent
4. `host.ipv6` for an old Agent

The legacy fallback preserves existing behavior but does not relabel a private
interface address as a discovered public egress address.

## Agent discovery

### Default behavior

Public-IP discovery is enabled by default. The first Agent run after install
performs discovery immediately. The Agent uses separate HTTPS endpoints for
IPv4 and IPv6 so a dual-stack server can report both families and a server
without IPv6 can still report IPv4.

The default endpoint order provides two independent services per family:

- IPv4: `https://api.ipify.org`, then `https://4.ident.me`
- IPv6: `https://api6.ipify.org`, then `https://6.ident.me`

ipify and ident.me document these family-specific HTTPS endpoints; ident.me
also explicitly recommends building redundancy. Operators may replace the
endpoint lists in configuration. Only `https` endpoints are accepted by
default; a localhost endpoint may be allowed explicitly for testing or a
self-hosted service.

Each response is limited to a small body, trimmed, parsed as an IP address,
and checked against the requested family. Loopback, private, link-local,
multicast, and unspecified values are rejected. Redirects are limited and may
not downgrade from HTTPS to HTTP.

IPv4 and IPv6 discovery run independently with a three-second timeout per
family. A failure is recorded only in local debug logs and does not fail report
construction or delivery.

### Configuration

The Agent configuration gains an optional section with secure defaults:

```toml
[collect.egress_ip]
enabled = true
refresh_interval = "6h"
timeout = "3s"
ipv4_endpoints = ["https://api.ipify.org", "https://4.ident.me"]
ipv6_endpoints = ["https://api6.ipify.org", "https://6.ident.me"]
```

Endpoint lists are tried in order, allowing an operator to configure multiple
providers or an internal endpoint without changing the binary. Invalid
durations or unsafe endpoint schemes fail configuration validation rather than
silently weakening transport security.

### Persistent cache

The Agent is a systemd oneshot process launched every minute, so successful
discovery results are cached in `/etc/net-probe/egress-ip-cache.json`, a path
already writable by the hardened service unit. The cache contains only the
canonical IPv4/IPv6 values and their last successful observation times.

On each run:

1. Read and validate the cache. A missing or malformed cache is treated as
   empty.
2. Refresh only a family whose successful observation is older than the
   configured interval.
3. Atomically replace the cache after any successful refresh.
4. If refresh fails, report the last validated cached value for that family
   and retry on the next one-minute run; do not advance its observation time.
5. If no cached value exists, omit that family and continue the report.

The cache file is installed with mode `0600`. The uninstaller removes it with
the existing `/etc/net-probe` directory cleanup.

## Panel ingestion and geolocation

### Address change detection

Add `ip_geo_ip TEXT` to `nodes`. It records the exact effective IP for which
the stored location fields were calculated.

After a valid Agent report is persisted, the report handler determines the
effective address. If it differs from `ip_geo_ip`, the Panel:

- records the new `ip_geo_ip`;
- clears the old `ip_location`, `ip_country`, `ip_region`, `ip_city`, and
  `ip_geo_updated_at` so stale geography is never shown for a new address;
- enqueues the node and address for asynchronous lookup.

The report acknowledgment does not wait for an external geolocation request.
An unreachable provider therefore cannot make an Agent report fail or time
out.

Private, loopback, link-local, multicast, unspecified, and otherwise
non-public fallback addresses are stored as `内网` without calling a provider.
The Agent's `egress_*` fields must already be public, but the Panel validates
them again because reports are untrusted input.

### Lookup worker

The Panel owns one bounded background worker and a deduplicating queue keyed by
IP address. A queue overflow is logged and is repaired by the periodic refresh
job; it does not affect report ingestion.

The worker:

1. Checks the persistent geolocation cache.
2. Calls the configured provider over HTTPS only when the cached result is
   missing or stale.
3. Stores normalized country, region, city, formatted location, and update
   time.
4. Updates every node still associated with that exact `ip_geo_ip`.

The final update includes `WHERE node_id = ? AND ip_geo_ip = ?` so a slow
response for an old address cannot overwrite a newer address's location.
Failed lookups keep the location empty and are logged without sensitive
response bodies.

### Persistent provider cache

Add an `ip_geo_cache` table keyed by canonical IP:

```sql
CREATE TABLE ip_geo_cache (
  ip TEXT PRIMARY KEY,
  location TEXT,
  country TEXT,
  region TEXT,
  city TEXT,
  updated_at INTEGER NOT NULL
);
```

This deduplicates lookups across nodes and survives Panel restarts. The
existing `[geo] refresh_interval` controls cache freshness as well as the
periodic reconciliation interval.

### Provider transport

Replace the hard-coded plaintext provider call with a small provider
interface and an HTTPS implementation. The zero-configuration default is
`https://ipwho.is/{ip}?lang=zh-CN`, which supports IPv4, IPv6, HTTPS, and
localized country/region/city fields. Its published free allowance is 1,000
requests per day without an API key; the persistent cache and single worker
keep normal small deployments well below that limit.

Panel configuration extends the existing section:

```toml
[geo]
refresh_interval = "12h"
provider = "ipwhois"
url = "https://ipwho.is/{ip}?lang=zh-CN"
timeout = "4s"
token_env = ""
```

Provider URL template and an optional bearer-token environment-variable name
are Panel configuration, not Agent report fields. The initial `ipwhois`
adapter accepts the default service and response-compatible keyed or
self-hosted endpoints. A provider with a different response schema requires a
new adapter behind the same interface, without changing the report contract.

The implementation limits response size, validates response status and
application-level `success`, applies a short timeout, and never logs tokens or
complete response bodies.

### Periodic reconciliation

The existing startup-and-periodic job remains as a repair path. It scans all
nodes, recomputes their effective address, and enqueues missing, mismatched, or
stale entries. Unlike the current implementation, the scan does not perform
network requests while holding database rows open.

This closes the existing startup race: a node arriving after the startup scan
is immediately enqueued by report ingestion, while the periodic scan repairs
queue overflow, transient failures, and pre-migration data.

## API and frontend

The existing node API continues to return the Host object and location fields.
TypeScript `Host` gains optional `egress_ipv4` and `egress_ipv6` properties.
No new admin endpoint is required.

The node list table uses these columns:

- `IP` becomes `出口 IP` and displays `egress_ipv4`, then `egress_ipv6`.
  For an old Agent it displays the legacy interface IP with a compatibility
  fallback, without modifying stored data.
- `IP出口地址` becomes `国家/地区` and displays `ip_location`.

Cards and node details use the same labels and selection rules. A missing
value displays `—`. The UI does not guess geography from the browser and does
not show a previous location after `ip_geo_ip` changes.

## Database migration

Migration is additive and idempotent:

- add `nodes.ip_geo_ip` when missing;
- create `ip_geo_cache` when missing;
- leave existing location columns and data intact;
- let startup reconciliation associate or refresh existing rows.

No destructive migration or report backfill is required.

## Failure behavior

| Failure | Result |
| --- | --- |
| Agent public-IP endpoint times out | Use the last validated cache value; otherwise omit the family. |
| Agent receives invalid/private data | Reject it, log a safe local diagnostic, and try the next configured endpoint. |
| Agent cache is malformed or unwritable | Continue collection; use a fresh valid result for this report when available. |
| Panel geolocation provider fails | Keep the current address but leave new geography empty; periodic reconciliation retries. |
| Agent reports a new IP while an old lookup is running | The conditional update discards the stale result. |
| Old Agent reports only interface IP | Preserve legacy lookup behavior; private values display as `内网`. |
| New Agent reports to an old Panel | Extra JSON fields are ignored. |

## Security and privacy

- All default discovery and geolocation requests use HTTPS.
- The Agent sends only canonical egress IP values, not raw provider responses.
- Provider secrets exist only in Panel environment/configuration and are never
  returned by APIs or written to logs.
- Response bodies are size-limited before parsing.
- Configurable URLs are validated to prevent accidental plaintext transport;
  redirects cannot downgrade transport.
- Discovery can be disabled for environments whose privacy policy forbids an
  external public-IP request.
- No health alert is generated merely because discovery or geolocation is
  unavailable.

## Testing

### Agent

- Parse and canonicalize valid IPv4 and IPv6 provider responses.
- Reject wrong-family, private, loopback, link-local, unspecified, oversized,
  malformed, and HTTP-error responses.
- Verify ordered endpoint fallback, timeout handling, and HTTPS redirect
  policy.
- Verify first-run discovery, fresh-cache reuse, stale-cache refresh, stale
  fallback after failure, corrupt-cache recovery, and atomic cache writes.
- Verify discovery failure does not fail `Build` or suppress sink delivery.
- Verify reports omit empty fields and never contain provider URLs or response
  bodies.
- Verify old configuration files receive enabled secure defaults.

### Panel

- Accept old and new report shapes.
- Prefer egress IPv4, then egress IPv6, then legacy addresses.
- Enqueue a new node immediately and enqueue again only when its effective IP
  changes or its cached geography expires.
- Clear stale geography on an IP change.
- Never call the provider for a non-public address.
- Deduplicate concurrent lookups and reuse the persistent cache.
- Prevent an old lookup result from overwriting a newer `ip_geo_ip`.
- Recover missing work during periodic reconciliation.
- Verify migration on fresh and existing databases.

### Frontend and end to end

- Verify table, cards, and detail views label and select address/location
  consistently.
- Verify old-Agent compatibility and empty states.
- Deploy an Agent on a public or NATed test host, confirm that the first report
  contains the discovered egress IP, and confirm that the Panel fills
  country/region without waiting for the 12-hour periodic run.
- Run Go tests and vet, frontend type checking, frontend tests if present, and
  the production build.

## Rollout

Deploy the Panel before Agents where practical so new reports are immediately
understood. Mixed versions remain safe in either order because all report and
database changes are additive. After rollout, verify one IPv4-only node and,
when available, one dual-stack node before relying on the new columns for
operations.

## External interface references

- [ipify API documentation](https://www.ipify.org/)
- [ident.me API documentation](https://api.ident.me/)
- [IPWhois geolocation API documentation](https://ipwhois.io/documentation)
