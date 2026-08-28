# Final Review Fix Wave Report

## Scope

Test-only hardening from review of commit `0ec732af600d83263ece9179bf7ee33d777aca0f`.
No production files or runtime behavior were changed.

## Changes

- `internal/detect/protocols_test.go`
  - Added coverage that an explicit Xray config path takes precedence over a valid fallback config path.
- `internal/detect/detect_test.go`
  - Added Detect-level coverage that protocol discovery failure leaves the Xray service in the result and passes the original missing-file error to `Deps.Logf`.
- `web/src/lib/serviceTelemetry.test.ts`
  - Added direct coverage for an `ok` zero online-client value, telemetry authority when a telemetry submetric is absent despite populated legacy stats, and `collector_not_implemented` normalization.
- `web/src/components/ServiceCard.test.tsx`
  - Changed the zero-online-client fixture to use nonzero traffic and asserted the labelled `在线连接` row contains `0`.
- `internal/panel/api/report_test.go`
  - Added assertions that both persisted telemetry submetrics retain state `ok` along with zero numeric values.

## Verification

The first attempt to run Go formatting with `gofmt` failed because the default shell PATH did not include Go. The bundled runtime was then used.

The first Go test attempt could not download modules under the sandbox network policy. The requested network-enabled rerun downloaded the missing modules. The final focused verification command was:

```sh
GOMODCACHE=/private/tmp/net-probe-gomodcache GOCACHE=/private/tmp/net-probe-gocache /Users/ziying.su/Documents/Codex/.tools/go/bin/go test ./internal/detect ./internal/panel/api && npm --prefix web test -- serviceTelemetry.test.ts ServiceCard.test.tsx && git diff --check
```

Result: exit code 0.

```text
ok   github.com/s2005lg/net-probe/internal/detect       (cached)
ok   github.com/s2005lg/net-probe/internal/panel/api    (cached)

Test Files  2 passed (2)
Tests       15 passed (15)
```

`git diff --check` completed without output.

The frontend dependency directory was incomplete for the initially selected package manager. This ignored dependency state was restored from the local npm cache with:

```sh
npm --prefix web install --offline --no-audit --no-fund
```

Result: 13 packages added; no tracked dependency manifests changed. An initial added display assertion expected `2 KB`; the formatter correctly renders `2.0 KB`, so the test-only expectation was corrected before the final passing run.
