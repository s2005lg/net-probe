#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d /tmp/net-probe-control-e2e.XXXXXX)"
trap 'rm -rf "$work_dir"' EXIT
cd "$repo_dir"

fixture_secret="control-e2e-secret-must-not-leak"
export NET_PROBE_E2E_FIXTURE_SECRET="$fixture_secret"

CGO_ENABLED=0 go build -trimpath -o "$work_dir/net-probe" ./cmd/net-probe
CGO_ENABLED=0 go build -trimpath -o "$work_dir/net-probe-panel" ./cmd/net-probe-panel

test_pattern='Test(ControlClientConnectsWithMTLSAndSendsHeartbeat|ControlClientProbeCompletesAfterAuthenticatedWelcome|RuntimeMaintainsControlSessionAlongsideReporting|RuntimeActionHandlersUseStrictPayloadsAndStableCodes|ExecutorReturnsDurablePriorResultAfterRestart|ExecutorRejectsForgeryTargetFutureAndChangedDuplicate|ExecutorResumesAcceptedCommandAfterItsOriginalTTL|OutboxEvictsOldestAtBothLimits|BootstrapFingerprintMismatchSendsNoEnrollmentSecret|RenewIfNeededAtomicallyReplacesNearExpiryCertificate|EnrollmentCodeConsumedOnce|EnrollmentExpiredReplayAndUnknownAreIndistinguishable|AdminRevocationImmediatelyBlocksAgentCertificate|RequireAgentRejectsRevokedAndExpiredDatabaseIdentity|ControlRejectsFirstNonHelloAndCertificateMismatch|HubRejectsCapacityAndReportsBackpressure|DispatcherLeavesOfflineQueuedAndMarksOnlyAfterHubEnqueue|QueuedForResendsRunningCommandAfterAgentReconnect|UpgradeManagerRejectsInsecureRedirect|ApplyVerifiedUpdateRollsBackOnProofOrRestartFailure|ApplyVerifiedUpdateDoesNotSwitchOnInstallOrInitialSwitchFailure|ValidateUpdateRequestRejectsUnknownFieldsTraversalAndSymlink|ValidateUpdateRequestRejectsWrongModeOwnerAndChangedArtifact|StrictDecodeRejectsInvalidCommandInput)$'

if ! go test -count=1 -timeout=4m \
  ./internal/controlproto ./internal/agent ./internal/panel/api ./internal/panel/control ./internal/panel/command ./internal/update \
  -run "$test_pattern" >"$work_dir/test.log" 2>&1; then
  tail -200 "$work_dir/test.log" >&2
  exit 1
fi
if grep -Fq -- "$fixture_secret" "$work_dir/test.log"; then
  echo "fixture secret leaked into E2E log" >&2
  exit 1
fi

echo "control e2e: PASS"
