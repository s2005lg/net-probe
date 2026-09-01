#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ci="$repo_dir/.github/workflows/ci.yml"
smoke="$repo_dir/tests/installers-smoke.sh"
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 missing: $2"; }

contains "$ci" "bash -n install.sh install-panel.sh tests/*.sh"
contains "$ci" "bash tests/release-contract.sh"
contains "$ci" "bash tests/ci-contract.sh"
contains "$ci" 'sudo env GITHUB_ACTIONS="$GITHUB_ACTIONS" RUNNER_OS="$RUNNER_OS" NET_PROBE_INSTALLER_SMOKE=1 bash tests/installers-smoke.sh'
contains "$ci" "npm run verify"
contains "$ci" "go-version: '1.25'"
contains "$ci" "bash tests/control-e2e.sh"
contains "$ci" "go test -race ./internal/agent ./internal/panel/control"
contains "$ci" "bash tests/agent-size.sh"
contains "$ci" 'NET_PROBE_RESOURCE_SMOKE: "1"'
contains "$ci" "bash tests/agent-resource.sh"
contains "$ci" "bash tests/secret-scan.sh"
contains "$ci" "go test ./..."
contains "$ci" "go vet ./..."
contains "$ci" "go build ./..."
contains "$smoke" 'GITHUB_ACTIONS:-'
contains "$smoke" 'RUNNER_OS:-'
contains "$smoke" 'NET_PROBE_INSTALLER_SMOKE:-'
contains "$smoke" 'unexpected curl call:'
contains "$smoke" 'net-probe-panel_linux_${NET_PROBE_TEST_ARCH}'
contains "$smoke" 'net-probe_linux_${NET_PROBE_TEST_ARCH}'
contains "$smoke" 'net-probe-update-helper_linux_${NET_PROBE_TEST_ARCH}'
echo "CI contract: PASS"
