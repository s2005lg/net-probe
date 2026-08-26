#!/usr/bin/env bash
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "installer smoke must run as root" >&2; exit 1; }

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d /tmp/net-probe-installer-smoke.XXXXXX)"
fake_bin="$test_dir/bin"
systemctl_log="$test_dir/systemctl.log"
mkdir -p "$fake_bin"

cat > "$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "$output" ]; then
  printf '#!/usr/bin/env bash\nexit 0\n' > "$output"
else
  printf '203.0.113.10'
fi
EOF
chmod +x "$fake_bin/curl"

cat > "$fake_bin/systemctl" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$systemctl_log"
EOF
chmod +x "$fake_bin/systemctl"
export PATH="$fake_bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 missing: $2"; }

NET_PROBE_PANEL_VERSION=v0.1.0 \
NET_PROBE_PANEL_PORT=24443 \
NET_PROBE_PANEL_AGENT_TOKEN=test-agent-token \
NET_PROBE_PANEL_ADMIN_PASSWORD=test-admin-password \
NET_PROBE_PANEL_PUBLIC_URL=https://panel.example.test:24443 \
  bash "$repo_dir/install-panel.sh" > "$test_dir/panel-install.out"

NET_PROBE_VERSION=v0.1.0 bash "$repo_dir/install.sh" > "$test_dir/agent-install.out"

[ -x /usr/local/bin/net-probe-panel ] || fail "panel binary is not executable"
[ -x /usr/local/bin/net-probe ] || fail "agent binary is not executable"
contains /etc/net-probe-panel/config.toml 'listen_addr = ":24443"'
contains /etc/net-probe-panel/config.toml 'token = "test-agent-token"'
contains /etc/net-probe/config.toml 'url = "https://127.0.0.1:24443"'
contains /etc/net-probe/config.toml 'tls_skip_verify = true'
contains /etc/net-probe/config.toml 'token_file = "/etc/net-probe/panel-token"'
[ "$(cat /etc/net-probe/panel-token)" = "test-agent-token" ] || fail "agent token mismatch"
[ "$(stat -c '%a' /etc/net-probe/panel-token)" = "600" ] || fail "agent token mode is not 600"
contains "$systemctl_log" 'enable --now net-probe-panel.service'
contains "$systemctl_log" 'enable --now net-probe.timer'
contains "$test_dir/panel-install.out" 'NET_PROBE_PANEL_URL="https://panel.example.test:24443"'
contains "$test_dir/panel-install.out" 'NET_PROBE_PANEL_TOKEN="test-agent-token" bash'

echo "installer smoke: PASS"
