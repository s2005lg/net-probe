#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 missing: $2"; }
not_contains() { ! grep -Fq -- "$2" "$1" || fail "$1 still contains: $2"; }

[ -f LICENSE ] || fail "LICENSE is missing"
contains LICENSE "MIT License"
contains LICENSE "Copyright (c) 2026 s2005lg"
contains LICENSE "Permission is hereby granted, free of charge"
not_contains README.md "v0.2.0"
contains README.md "NET_PROBE_PANEL_VERSION=v0.1.0"
contains .github/workflows/release.yml "sha256sum net-probe_linux_amd64 net-probe_linux_arm64 net-probe-panel_linux_amd64 net-probe-panel_linux_arm64 > SHA256SUMS"
contains .github/workflows/release.yml "sha256sum --check SHA256SUMS"
contains .github/workflows/release.yml "npm run verify"

publish_files="$(awk '
  /^[[:space:]]*- name: Publish release$/ { in_publish = 1; next }
  in_publish && /^[[:space:]]+files: \|$/ { in_files = 1; next }
  in_files && /^            [^[:space:]]/ {
    line = $0
    sub(/^            /, "", line)
    print line
    next
  }
  in_files { exit }
' .github/workflows/release.yml)"
for artifact in \
  net-probe_linux_amd64 \
  net-probe_linux_arm64 \
  net-probe-panel_linux_amd64 \
  net-probe-panel_linux_arm64 \
  SHA256SUMS; do
  grep -Fxq -- "$artifact" <<<"$publish_files" || fail "release upload missing: $artifact"
done

echo "release contract: PASS"
