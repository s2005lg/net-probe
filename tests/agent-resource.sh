#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

if [ "${NET_PROBE_RESOURCE:-}" = "1" ]; then
  duration="${NET_PROBE_RESOURCE_SECONDS:-600}"
  minimum_duration=600
elif [ "${NET_PROBE_RESOURCE_SMOKE:-}" = "1" ]; then
  duration=5
  minimum_duration=5
else
  echo "agent resource: SKIP (set NET_PROBE_RESOURCE=1 for the 10-minute release gate)"
  exit 0
fi
if [ "$(uname -s)" != "Linux" ] || [ ! -r /proc/self/stat ]; then
  echo "agent resource gate requires Linux /proc" >&2
  exit 1
fi

if ! [[ "$duration" =~ ^[0-9]+$ ]] || [ "$duration" -lt "$minimum_duration" ]; then
  echo "agent resource duration is below the selected gate minimum" >&2
  exit 1
fi

work_dir="$(mktemp -d /tmp/net-probe-agent-resource.XXXXXX)"
panel_pid=""
agent_pid=""
cleanup() {
  [ -z "$agent_pid" ] || kill "$agent_pid" >/dev/null 2>&1 || true
  [ -z "$panel_pid" ] || kill "$panel_pid" >/dev/null 2>&1 || true
  wait "$agent_pid" >/dev/null 2>&1 || true
  wait "$panel_pid" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

public_hex=d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a
port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"
panel_url="https://127.0.0.1:${port}"

go build -trimpath -ldflags="-s -w -X main.version=v1.2.3 -X main.releasePublicKeyHex=${public_hex}" -o "$work_dir/net-probe-panel" ./cmd/net-probe-panel
go build -trimpath -gcflags=all=-l -ldflags="-s -w -buildid= -X main.version=v1.2.3 -X main.releasePublicKeyHex=${public_hex}" -o "$work_dir/net-probe" ./cmd/net-probe

mkdir -p "$work_dir/panel-data" "$work_dir/agent-config/net-probe/pki" "$work_dir/agent-state"
cat > "$work_dir/panel.toml" <<EOF
listen_addr = ":${port}"
data_dir = "${work_dir}/panel-data"
public_url = "${panel_url}"
node_timeout = "3m"

[admin]
user = "admin"
EOF
NET_PROBE_PANEL_ADMIN_PASSWORD=resource-gate-password "$work_dir/net-probe-panel" --config "$work_dir/panel.toml" >"$work_dir/panel.log" 2>&1 &
panel_pid=$!

for _attempt in $(seq 1 100); do
  if curl -kfsS "${panel_url}/api/v1/ca" -o "$work_dir/ca.crt"; then
    break
  fi
  kill -0 "$panel_pid" >/dev/null 2>&1 || { echo "resource Panel exited" >&2; exit 1; }
  sleep 0.1
done
[ -s "$work_dir/ca.crt" ] || { echo "resource Panel did not become ready" >&2; exit 1; }

curl -kfsS -c "$work_dir/cookies" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"resource-gate-password"}' \
  "${panel_url}/api/v1/admin/login" >/dev/null
curl -kfsS -b "$work_dir/cookies" -H 'Content-Type: application/json' \
  -d '{"label":"resource gate","expires_in_seconds":600}' \
  "${panel_url}/api/v1/admin/enrollments" > "$work_dir/enrollment.json"
mapfile -t enrollment < <(python3 - "$work_dir/enrollment.json" <<'PY'
import json, pathlib, sys
data = json.loads(pathlib.Path(sys.argv[1]).read_text())
print(data["code"])
print(data["ca_fingerprint"])
PY
)
[ "${#enrollment[@]}" -eq 2 ] || { echo "resource enrollment response is invalid" >&2; exit 1; }

cat > "$work_dir/agent-config/net-probe/config.toml" <<EOF
[agent]
node_id = "resource-gate-agent"
log_level = "error"
report_interval = "1h"
collect_timeout = "45s"
shutdown_timeout = "20s"

[panel]
url = "${panel_url}"
ca_file = "${work_dir}/agent-config/net-probe/pki/ca.crt"
cert_file = "${work_dir}/agent-config/net-probe/pki/agent.crt"
key_file = "${work_dir}/agent-config/net-probe/pki/agent.key"
command_key_file = "${work_dir}/agent-config/net-probe/pki/command-signing.pub"
release_key_file = "${work_dir}/agent-config/net-probe/pki/release-signing.pub"

[collect]
disk_mounts = ["/"]
upgradable = false
EOF
printf '%s\n' "${enrollment[0]}" | env XDG_CONFIG_HOME="$work_dir/agent-config" XDG_STATE_HOME="$work_dir/agent-state" \
  "$work_dir/net-probe" enroll --panel-url "$panel_url" --ca-fingerprint "${enrollment[1]}" --code-stdin >/dev/null

env XDG_CONFIG_HOME="$work_dir/agent-config" XDG_STATE_HOME="$work_dir/agent-state" \
  "$work_dir/net-probe" --config "$work_dir/agent-config/net-probe/config.toml" >"$work_dir/agent.log" 2>&1 &
agent_pid=$!
for _attempt in $(seq 1 600); do
  kill -0 "$agent_pid" >/dev/null 2>&1 || { echo "resource Agent exited during warmup" >&2; exit 1; }
  if curl -kfsS -b "$work_dir/cookies" "${panel_url}/api/v1/admin/nodes" > "$work_dir/nodes.json" &&
     grep -Fq '"node_id":"resource-gate-agent"' "$work_dir/nodes.json"; then
    break
  fi
  sleep 0.1
done
grep -Fq '"node_id":"resource-gate-agent"' "$work_dir/nodes.json" || { echo "resource Agent did not finish its initial report" >&2; exit 1; }
sleep 2

clock_ticks="$(getconf CLK_TCK)"
start_ticks="$(awk '{print $14 + $15}' "/proc/${agent_pid}/stat")"
sleep "$duration"
kill -0 "$agent_pid" >/dev/null 2>&1 || { echo "resource Agent exited while idle" >&2; exit 1; }
end_ticks="$(awk '{print $14 + $15}' "/proc/${agent_pid}/stat")"
rss_kib="$(awk '/^VmRSS:/ {print $2}' "/proc/${agent_pid}/status")"
cpu_milli_pct=$(( (end_ticks - start_ticks) * 100000 / clock_ticks / duration ))

printf 'agent resource duration=%ss rss=%sKiB average_cpu=%d.%03d%%\n' \
  "$duration" "$rss_kib" "$((cpu_milli_pct / 1000))" "$((cpu_milli_pct % 1000))"
[ "$rss_kib" -le 30720 ] || { echo "Agent idle RSS exceeds 30 MiB" >&2; exit 1; }
[ "$cpu_milli_pct" -lt 500 ] || { echo "Agent average idle CPU is not below 0.5%" >&2; exit 1; }

echo "agent resource: PASS"
