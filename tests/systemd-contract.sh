#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 missing: $2"; }
not_contains() { ! grep -Fq -- "$2" "$1" || fail "$1 still contains: $2"; }

for unit_source in systemd/net-probe.service install.sh; do
  contains "$unit_source" "Type=notify"
  contains "$unit_source" "NotifyAccess=main"
  contains "$unit_source" "StateDirectory=net-probe"
  contains "$unit_source" "RuntimeDirectory=net-probe"
  contains "$unit_source" "Restart=on-failure"
  contains "$unit_source" "WatchdogSec="
  contains "$unit_source" "TimeoutStopSec=30s"
  contains "$unit_source" "NET_PROBE_UPDATE_DIRECTORY=/var/lib/net-probe-updates"
  contains "$unit_source" "ReadWritePaths=/etc/net-probe/pki /var/lib/net-probe-updates"
done
not_contains systemd/net-probe.service "Type=oneshot"

contains install.sh "enable --now net-probe.service"
contains install.sh "enable --now net-probe-update.path"
contains install.sh "install -d -o root -g net-probe -m 0770 /var/lib/net-probe-updates"
contains install.sh 'openssl pkeyutl -verify'
contains install.sh 'NET_PROBE_RELEASE_PUBLIC_KEY_HEX'
contains install.sh '"$download_path" --config "$stage_agent_dir/config.toml" --preflight'
not_contains install.sh '$download_path --version'
contains install.sh 'ln -sfn "$version_dir/net-probe" /usr/local/bin/net-probe'
not_contains install.sh "enable --now net-probe.timer"

contains systemd/net-probe-update.path "PathExists=/var/lib/net-probe-updates/pending.json"
contains systemd/net-probe-update.path "PathExists=/var/lib/net-probe-updates/claimed.json"

echo "systemd contract: PASS"
