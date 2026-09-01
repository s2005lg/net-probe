#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

if [ "${NET_PROBE_CAPACITY:-}" != "1" ]; then
  echo "capacity control: SKIP (set NET_PROBE_CAPACITY=1 for tag/manual gate)"
  exit 0
fi

NET_PROBE_CAPACITY=1 go test ./internal/panel/control \
  -run '^TestHubCapacityOneThousandSessions$' -count=1 -timeout=3m -v

echo "capacity control: PASS"
