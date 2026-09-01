#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
work_dir="$(mktemp -d /tmp/net-probe-agent-size.XXXXXX)"
baseline_dir="$work_dir/baseline"
cleanup() {
  git worktree remove --force "$baseline_dir" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

base_ref="${NET_PROBE_SIZE_BASE_REF:-}"
if [ -z "$base_ref" ]; then
  if git rev-parse --verify origin/main >/dev/null 2>&1; then
    base_ref="$(git merge-base HEAD origin/main)"
  else
    base_ref="$(git rev-parse HEAD^)"
  fi
fi
git worktree add --detach "$baseline_dir" "$base_ref" >/dev/null

file_size() {
  if stat -c '%s' "$1" >/dev/null 2>&1; then
    stat -c '%s' "$1"
  else
    stat -f '%z' "$1"
  fi
}

for arch in amd64 arm64; do
  baseline="$work_dir/net-probe-baseline-$arch"
  candidate="$work_dir/net-probe-candidate-$arch"
  (cd "$baseline_dir" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -gcflags=all=-l -ldflags='-s -w -buildid=' -o "$baseline" ./cmd/net-probe)
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -gcflags=all=-l -ldflags='-s -w -buildid=' -o "$candidate" ./cmd/net-probe
  baseline_size="$(file_size "$baseline")"
  candidate_size="$(file_size "$candidate")"
  growth=$((candidate_size - baseline_size))
  gzip_size="$(gzip -9 -c "$candidate" | wc -c | tr -d ' ')"
  printf 'agent linux/%s raw=%s gzip=%s baseline=%s growth=%s\n' \
    "$arch" "$candidate_size" "$gzip_size" "$baseline_size" "$growth"
  if [ "$candidate_size" -gt 7000000 ]; then
    echo "Agent raw binary exceeds 7,000,000 bytes for $arch" >&2
    exit 1
  fi
  if [ "$gzip_size" -gt 3000000 ]; then
    echo "Agent gzip artifact exceeds 3,000,000 bytes for $arch" >&2
    exit 1
  fi
  if [ "$growth" -gt 500000 ]; then
    echo "Agent raw binary grew by more than 500,000 bytes for $arch" >&2
    exit 1
  fi
done

echo "agent size: PASS"
