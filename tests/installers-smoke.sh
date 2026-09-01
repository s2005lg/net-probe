#!/usr/bin/env bash
set -euo pipefail

if [ "${GITHUB_ACTIONS:-}" != "true" ] ||
   [ "${RUNNER_OS:-}" != "Linux" ] ||
   [ "${NET_PROBE_INSTALLER_SMOKE:-}" != "1" ]; then
  echo "installer smoke is restricted to the opted-in GitHub Actions Linux runner" >&2
  exit 1
fi
[ "$(id -u)" -eq 0 ] || { echo "installer smoke must run as root" >&2; exit 1; }

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d /tmp/net-probe-installer-smoke.XXXXXX)"
fake_bin="$test_dir/bin"
systemctl_log="$test_dir/systemctl.log"
curl_log="$test_dir/curl.log"
mkdir -p "$fake_bin"

case "$(uname -m)" in
  x86_64|amd64) test_arch="amd64" ;;
  aarch64|arm64) test_arch="arm64" ;;
  *) echo "unsupported test architecture: $(uname -m)" >&2; exit 1 ;;
esac
export NET_PROBE_TEST_ARCH="$test_arch"
export NET_PROBE_CURL_LOG="$curl_log"

cat > "$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    --proto|--proto-redir|--connect-timeout) shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
printf '%s -> %s\n' "$url" "$output" >> "$NET_PROBE_CURL_LOG"
agent_body='#!/usr/bin/env bash
if [ "${1:-}" = "enroll" ]; then
  pki="${XDG_CONFIG_HOME}/net-probe/pki"
  mkdir -p "$pki"
  for name in agent.key agent.crt ca.crt command-signing.pub release-signing.pub identity.json; do
    printf "test-%s\n" "$name" > "$pki/$name"
  done
  exit 0
fi
exit 0
'
helper_body='#!/usr/bin/env bash
exit 0
'
case "$url" in
  "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-panel_linux_${NET_PROBE_TEST_ARCH}")
    printf '#!/usr/bin/env bash\nexit 0\n' > "$output"
    ;;
  "https://127.0.0.1:24443/api/v1/ca")
    printf '%s\n' '-----BEGIN CERTIFICATE-----' 'test' '-----END CERTIFICATE-----' > "$output"
    ;;
  "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe_linux_${NET_PROBE_TEST_ARCH}.manifest.json")
    size="$(printf '%s' "$agent_body" | wc -c | tr -d ' ')"
    digest="$(printf '%s' "$agent_body" | sha256sum | awk '{print $1}')"
    printf '{"version":"v0.1.0","os":"linux","arch":"%s","byte_size":%s,"sha256":"%s","artifact_url":"https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe_linux_%s","minimum_panel_version":"v0.1.0","control_version":"1","issued_at":1,"expires_at":2}\n' "$NET_PROBE_TEST_ARCH" "$size" "$digest" "$NET_PROBE_TEST_ARCH" > "$output"
    ;;
  "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe_linux_${NET_PROBE_TEST_ARCH}.manifest.sig")
    head -c 64 /dev/zero | base64 > "$output"
    ;;
  "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe_linux_${NET_PROBE_TEST_ARCH}")
    printf '%s' "$agent_body" > "$output"
    ;;
  "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-update-helper_linux_${NET_PROBE_TEST_ARCH}.manifest.json")
    size="$(printf '%s' "$helper_body" | wc -c | tr -d ' ')"
    digest="$(printf '%s' "$helper_body" | sha256sum | awk '{print $1}')"
    printf '{"version":"v0.1.0","os":"linux","arch":"%s","byte_size":%s,"sha256":"%s","artifact_url":"https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-update-helper_linux_%s","minimum_panel_version":"v0.1.0","control_version":"1","issued_at":1,"expires_at":2}\n' "$NET_PROBE_TEST_ARCH" "$size" "$digest" "$NET_PROBE_TEST_ARCH" > "$output"
    ;;
  "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-update-helper_linux_${NET_PROBE_TEST_ARCH}.manifest.sig")
    head -c 64 /dev/zero | base64 > "$output"
    ;;
  "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-update-helper_linux_${NET_PROBE_TEST_ARCH}")
    printf '%s' "$helper_body" > "$output"
    ;;
  *) echo "unexpected curl call: $url -> $output" >&2; exit 1 ;;
esac
if [[ "$url" == *net-probe*_linux_* && "$url" != *.json && "$url" != *.sig ]]; then
  chmod +x "$output"
fi
EOF
chmod +x "$fake_bin/curl"

cat > "$fake_bin/openssl" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = "version" ] && echo 'OpenSSL 3.0.0 test'
exit 0
EOF
chmod +x "$fake_bin/openssl"

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
NET_PROBE_PANEL_ADMIN_PASSWORD=test-admin-password \
NET_PROBE_PANEL_PUBLIC_URL=https://panel.example.test:24443 \
  bash "$repo_dir/install-panel.sh" > "$test_dir/panel-install.out"

# A failed migration may download and stage, but must not modify production
# paths or disable the old timer before enrollment/report/control preflight.
mkdir -p /etc/net-probe /etc/systemd/system
rm -f /usr/local/bin/net-probe
printf 'old-agent-binary\n' > /usr/local/bin/net-probe
chmod 0755 /usr/local/bin/net-probe
printf 'old-agent-config\n' > /etc/net-probe/config.toml
printf 'old-agent-timer\n' > /etc/systemd/system/net-probe.timer
old_binary_sha="$(sha256sum /usr/local/bin/net-probe | awk '{print $1}')"
old_config_sha="$(sha256sum /etc/net-probe/config.toml | awk '{print $1}')"
old_timer_sha="$(sha256sum /etc/systemd/system/net-probe.timer | awk '{print $1}')"
if NET_PROBE_VERSION=v0.1.0 \
   NET_PROBE_RELEASE_PUBLIC_KEY_HEX=0000000000000000000000000000000000000000000000000000000000000000 \
   NET_PROBE_PANEL_URL=https://panel.example.test:24443 \
   NET_PROBE_CA_FINGERPRINT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
     bash "$repo_dir/install.sh" > "$test_dir/agent-failed-install.out" 2>&1; then
  fail "Agent install without enrollment code unexpectedly succeeded"
fi
[ "$(sha256sum /usr/local/bin/net-probe | awk '{print $1}')" = "$old_binary_sha" ] || fail "failed preflight changed old Agent binary"
[ "$(sha256sum /etc/net-probe/config.toml | awk '{print $1}')" = "$old_config_sha" ] || fail "failed preflight changed old Agent config"
[ "$(sha256sum /etc/systemd/system/net-probe.timer | awk '{print $1}')" = "$old_timer_sha" ] || fail "failed preflight changed old Agent timer"
! grep -Fq 'disable --now net-probe.timer' "$systemctl_log" || fail "failed preflight disabled old timer"

NET_PROBE_VERSION=v0.1.0 \
NET_PROBE_RELEASE_PUBLIC_KEY_HEX=0000000000000000000000000000000000000000000000000000000000000000 \
NET_PROBE_PANEL_URL=https://panel.example.test:24443 \
NET_PROBE_CA_FINGERPRINT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
NET_PROBE_ENROLLMENT_CODE=test-one-use-enrollment-code \
  bash "$repo_dir/install.sh" > "$test_dir/agent-install.out"

[ -x /usr/local/bin/net-probe-panel ] || fail "panel binary is not executable"
[ -x /usr/local/bin/net-probe ] || fail "agent binary is not executable"
[ -x /usr/local/libexec/net-probe-update-helper ] || fail "update helper is not executable"
contains /etc/net-probe-panel/config.toml 'listen_addr = ":24443"'
contains /etc/net-probe-panel/config.toml 'public_url = "https://panel.example.test:24443"'
! grep -Fq 'token =' /etc/net-probe-panel/config.toml || fail "legacy Panel Agent token remains"
contains /etc/net-probe/config.toml '[panel]'
contains /etc/net-probe/config.toml 'url = "https://panel.example.test:24443"'
! grep -Fq 'tls_skip_verify' /etc/net-probe/config.toml || fail "legacy TLS bypass remains"
! grep -Fq 'token_file' /etc/net-probe/config.toml || fail "legacy shared token remains"
[ "$(stat -c '%U:%G:%a' /etc/net-probe/trust/command-signing.pub)" = "root:root:644" ] || fail "root command trust ownership mismatch"
contains "$systemctl_log" 'enable --now net-probe-panel.service'
contains "$systemctl_log" 'enable --now net-probe.service'
contains "$systemctl_log" 'enable --now net-probe-update.path'
contains "$systemctl_log" 'is-active --quiet net-probe.service'
contains "$repo_dir/install.sh" '"$download_path" --config "$stage_agent_dir/config.toml" --preflight'
contains "$curl_log" "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-panel_linux_${test_arch} -> /usr/local/bin/net-probe-panel"
contains "$curl_log" "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe_linux_${test_arch}.manifest.json"
contains "$curl_log" "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe_linux_${test_arch}.manifest.sig"
contains "$curl_log" "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe_linux_${test_arch}"
contains "$curl_log" "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-update-helper_linux_${test_arch}.manifest.json"
contains "$curl_log" "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-update-helper_linux_${test_arch}.manifest.sig"
contains "$curl_log" "https://github.com/s2005lg/net-probe/releases/download/v0.1.0/net-probe-update-helper_linux_${test_arch}"
[ -L /usr/local/bin/net-probe ] || fail "agent binary is not a version-store symlink"
[ "$(readlink /usr/local/bin/net-probe)" = "/opt/net-probe/versions/v0.1.0/net-probe" ] || fail "agent symlink target mismatch"
[ ! -L /usr/local/libexec/net-probe-update-helper ] || fail "update helper must be a fixed root-owned executable"
[ "$(stat -c '%U:%G:%a' /usr/local/libexec/net-probe-update-helper)" = "root:root:755" ] || fail "update helper ownership mismatch"
contains "$test_dir/panel-install.out" 'Panel CA fingerprint:'
contains "$test_dir/panel-install.out" 'NET_PROBE_ENROLLMENT_CODE=<one-use-code>'

echo "installer smoke: PASS"
