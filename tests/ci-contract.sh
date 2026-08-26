#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ci="$repo_dir/.github/workflows/ci.yml"
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 missing: $2"; }

contains "$ci" "bash -n install.sh install-panel.sh tests/*.sh"
contains "$ci" "bash tests/release-contract.sh"
contains "$ci" "sudo bash tests/installers-smoke.sh"
contains "$ci" "npm run verify"
contains "$ci" "go test ./..."
contains "$ci" "go vet ./..."
contains "$ci" "go build ./..."
echo "CI contract: PASS"
