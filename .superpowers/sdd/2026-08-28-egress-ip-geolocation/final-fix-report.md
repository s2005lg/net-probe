# Final whole-branch review fix report

## Scope and commits

- Review base: `1b7871ea71d4e881d99b0fd51ec2abb1956a3096`
- Implementation commit: `396c325807b4c6377ebd981ea1886864ec2c4a19` (`fix: resolve egress final review blockers`)
- This report is delivered in a separate documentation-only commit after the verified implementation commit.
- No merge, tag, push, staging access, or staging deployment was performed. Hysteria2 deployment files were not modified.

## Implemented behavior

### 1. Real metrics wire contract

- The frontend `Metric.services_json` contract now accepts the Go handler's primary JSON-array wire shape.
- `aggregateTraffic` consumes arrays directly, so capability-aware traffic samples remain eligible and no longer degrade to unknown coverage because of a second `JSON.parse`.
- String-encoded snapshots remain supported for older Panel responses.
- The Go handler contract test verifies that `services_json` leaves the real `/metrics` handler as a JSON array, while the frontend regression tests cover both array and legacy string inputs.

### 2. Public-routable address classification

- Added one dependency-free shared `netaddr.IsPublic` classifier used by Agent discovery and Panel Geo eligibility.
- IPv4-mapped IPv6 addresses are unmapped before family checks and classification.
- The classifier rejects private, loopback, link-local, multicast, unspecified, CGNAT, documentation, benchmarking, reserved, translation, discard-only, deprecated, and other IANA special-purpose IPv4/IPv6 prefixes represented as global unicast by Go.
- Agent and Panel table coverage includes CGNAT `100.64.0.0/10`, all three IPv4 documentation blocks, `198.18.0.0/15`, `2001:db8::/32`, additional special-use blocks, and mapped IPv4 canonicalization.

### 3. Bounded failure backoff

- Agent discovery tracks the last successful address/observation separately from attempt, failure-count, and retry-at state for each family.
- Failed family discovery retries after 5 minutes, doubles on consecutive failures, and is capped at 6 hours. Retry state is persisted in the existing `egress-ip-cache.json`, so one-minute one-shot Agent restarts do not cause an IPv6 request storm on an IPv4-only host.
- Last successful Agent addresses remain reportable during stale-refresh failures. Old success-only cache JSON remains readable because all retry fields are additive and optional.
- Panel Geo stores attempt/failure/retry state per IP in the existing SQLite cache. Provider failure preserves the last successful location; repeated reports inside the retry window do not call the provider; retry occurs at the persisted deadline and success resets failure state.
- English and Chinese README sections document the 5-minute exponential retry, 6-hour cap, persistence, and last-success retention.
- Discovery and Geo failures remain non-fatal to report delivery and service health.

### 4. Invalid telemetry handling

- sing-box now distinguishes required `up`/`down` fields from zero values using pointer decoding. Missing fields, wrong types, negatives, and other invalid numeric values produce `invalid_response`; explicit zero remains `ok`.
- Hysteria2 traffic entries require explicit `tx` and `rx` fields. Null/malformed traffic or online-client structures produce `invalid_response`; valid empty collections and explicit zero counters remain available telemetry.
- Xray traffic requires at least one recognized counter and validates every recognized numeric value. Unknown-only, missing-value, nonnumeric, negative, or ambiguous counter output produces `invalid_response`.
- Xray online-client JSON now requires `users`, each user's `ips`, and each IP value; missing/null/malformed structure produces `invalid_response`, while explicit empty arrays remain authoritative zero.

### 5. Redirect policy on every hop

- Agent configuration validation and discovery redirects now share one URL policy: HTTPS is allowed; HTTP is allowed only for exact loopback hosts `localhost`, `127.0.0.1`, and `::1`.
- Every redirect target is checked. In particular, `http://localhost` cannot redirect to external cleartext HTTP.
- HTTPS-to-HTTP rejection and the existing three-redirect limit remain intact. Loopback HTTP-to-loopback HTTP and redirects to HTTPS remain allowed.

### 6. Canonical effective IP from API to UI

- Panel node list/detail/patch responses now expose `effective_ip`, computed with the same `geo.EffectiveIP` function used for Geo identity.
- Field-specific family validation rejects an IPv6 value in an IPv4 field and vice versa, falls through malformed values, and canonicalizes mapped IPv4.
- Node views display the backend canonical field whenever the new Panel supplies it. An explicitly empty canonical value displays `—` rather than reviving malformed raw host data.
- When `effective_ip` is absent, the UI retains the legacy old-Panel fallback through old Agent host fields.
- API and UI tests cover malformed egress, family mismatch, mapped IPv4, legacy fallback, old Panel omission, and explicit empty canonical output.

### 7. Final-review minors

- Geo queue saturation emits a fixed logger event after releasing the queue mutex. The event contains neither the queued IP/value nor provider bodies, tokens, URLs, or other secrets; overflow remains non-blocking and retryable.
- Migration adds retry columns to a pre-existing `ip_geo_cache` with zero defaults and creates `idx_nodes_ip_geo_ip` only after legacy `nodes` tables receive the `ip_geo_ip` column.
- `CREATE INDEX IF NOT EXISTS` makes the index migration idempotent. Migration coverage runs twice against a legacy schema, preserves existing node/cache rows, validates the additive column shape/defaults, and verifies SQLite selects the Geo index.

## Compatibility and invariants

- Report `schema_version` remains `"1"`; no report contract version was changed.
- Old Agent reports remain accepted. New node API fields are additive.
- Old Panel responses remain usable by the frontend through the absence-sensitive legacy address fallback.
- Existing Agent cache files and Panel databases migrate in place without replacing successful address/location values.
- Report persistence ACK semantics were not changed.
- No new third-party runtime dependency was added.

## Verification

All final commands completed with exit status 0 after the last implementation change:

- `gofmt -w` on every changed Go source and test file.
- `GOCACHE=/tmp/net-probe-go-cache go test -race -count=1 ./...`
- `GOCACHE=/tmp/net-probe-go-cache go vet ./...`
- `npm --prefix web run verify` — 26 Vitest tests passed; TypeScript typecheck, production build, and bundle check passed.
- `GOCACHE=/tmp/net-probe-go-cache go test -race -count=1 ./internal/agent ./internal/panel/api -run '^(TestPanelIntegration|TestReportToGeoIntegration|TestNodeMetrics|TestNodeAPIEffectiveIP)$'`
- `git diff --check`

The implementation tree was clean immediately after commit `396c325807b4c6377ebd981ea1886864ec2c4a19`. Final clean status is rechecked after committing this report.

## Explicitly deferred pre-existing issue

Report persistence ACK semantics remain deferred exactly as directed. A node or metric persistence failure can still receive an ACK. This predates the egress/Geo work and needs a separate transaction and HTTP-error design; it was not modified in this fix.

## Concerns

- None in the scoped implementation.
- Staging verification remains intentionally pending for the separate scoped re-review turn.
