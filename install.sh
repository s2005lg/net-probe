#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "run as root: curl -fsSL .../install.sh | sudo bash" >&2
  exit 1
fi

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

version="${NET_PROBE_VERSION:-}"
if [ -z "$version" ] || [ "$version" = "latest" ]; then
  echo "NET_PROBE_VERSION must name the explicit signed release (for example v1.2.3); latest is refused to prevent replay" >&2
  exit 1
fi
release_public_key_hex="${NET_PROBE_RELEASE_PUBLIC_KEY_HEX:-}"
if [ "${#release_public_key_hex}" -ne 64 ]; then
  echo "NET_PROBE_RELEASE_PUBLIC_KEY_HEX must be the trusted 32-byte lowercase Ed25519 public key" >&2
  exit 1
fi
case "$release_public_key_hex" in
  *[!0-9a-f]*) echo "NET_PROBE_RELEASE_PUBLIC_KEY_HEX must be lowercase hexadecimal" >&2; exit 1 ;;
esac
base="https://github.com/s2005lg/net-probe/releases/download/${version}"

for required_command in curl openssl python3 base64 sha256sum stat; do
  command -v "$required_command" >/dev/null 2>&1 || { echo "missing required command: $required_command" >&2; exit 1; }
done
openssl version | grep -Eq '^OpenSSL (3|[4-9])\.' || { echo "OpenSSL 3.0 or newer is required for Ed25519 verification" >&2; exit 1; }
staging_dir="$(mktemp -d /tmp/net-probe-install.XXXXXX)"
trap 'rm -rf "$staging_dir"' EXIT
manifest_path="$staging_dir/manifest.json"
signature_text_path="$staging_dir/manifest.sig.b64"
signature_path="$staging_dir/manifest.sig"
public_key_path="$staging_dir/release-public.der"
download_path="$staging_dir/net-probe"
curl --proto '=https' --proto-redir '=https' -fLsS "${base}/net-probe_linux_${arch}.manifest.json" -o "$manifest_path"
curl --proto '=https' --proto-redir '=https' -fLsS "${base}/net-probe_linux_${arch}.manifest.sig" -o "$signature_text_path"
python3 - "$release_public_key_hex" "$public_key_path" "$manifest_path" <<'PY'
import pathlib, sys
key = bytes.fromhex(sys.argv[1])
if len(key) != 32:
    raise SystemExit("invalid release public key")
pathlib.Path(sys.argv[2]).write_bytes(bytes.fromhex("302a300506032b6570032100") + key)
manifest_path = pathlib.Path(sys.argv[3])
manifest = manifest_path.read_bytes()
if not manifest.endswith(b"\n") or manifest[:-1].strip() != manifest[:-1]:
    raise SystemExit("release manifest encoding is invalid")
manifest_path.write_bytes(manifest[:-1])
PY
base64 --decode < "$signature_text_path" > "$signature_path"
openssl pkeyutl -verify -pubin -inkey "$public_key_path" -keyform DER -rawin -in "$manifest_path" -sigfile "$signature_path" >/dev/null
mapfile -t manifest_fields < <(python3 - "$manifest_path" "$arch" <<'PY'
import json, pathlib, re, sys, time, urllib.parse
raw = pathlib.Path(sys.argv[1]).read_bytes()
manifest = json.loads(raw)
expected = {"version","os","arch","byte_size","sha256","artifact_url","minimum_panel_version","control_version","issued_at","expires_at"}
if set(manifest) != expected or manifest["os"] != "linux" or manifest["arch"] != sys.argv[2]:
    raise SystemExit("release manifest platform/schema mismatch")
if not re.fullmatch(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", manifest["version"]):
    raise SystemExit("release manifest version is invalid")
if not isinstance(manifest["byte_size"], int) or not 0 < manifest["byte_size"] <= 32 * 1024 * 1024:
    raise SystemExit("release manifest size is invalid")
if not re.fullmatch(r"[0-9a-f]{64}", manifest["sha256"]):
    raise SystemExit("release manifest hash is invalid")
if manifest["control_version"] != "1" or not isinstance(manifest["issued_at"], int) or not isinstance(manifest["expires_at"], int):
    raise SystemExit("release manifest time/control fields are invalid")
if manifest["expires_at"] <= manifest["issued_at"] or manifest["expires_at"] - manifest["issued_at"] > 24 * 60 * 60:
    raise SystemExit("release manifest validity interval is invalid")
if manifest["issued_at"] > int(time.time()) + 5 * 60:
    raise SystemExit("release manifest was issued in the future")
url = urllib.parse.urlsplit(manifest["artifact_url"])
expected_path = f"/s2005lg/net-probe/releases/download/{manifest['version']}/net-probe_linux_{manifest['arch']}"
if url.scheme != "https" or url.netloc != "github.com" or url.path != expected_path or url.query or url.fragment:
    raise SystemExit("release artifact URL is invalid")
print(manifest["version"])
print(manifest["byte_size"])
print(manifest["sha256"])
print(manifest["artifact_url"])
PY
)
[ "${#manifest_fields[@]}" -eq 4 ] || { echo "release manifest validation failed" >&2; exit 1; }
resolved_version="${manifest_fields[0]}"
expected_size="${manifest_fields[1]}"
expected_sha256="${manifest_fields[2]}"
artifact_url="${manifest_fields[3]}"
if [ "$resolved_version" != "$version" ]; then
  echo "release manifest version does not match requested version" >&2
  exit 1
fi
curl --proto '=https' --proto-redir '=https' -fLsS "$artifact_url" -o "$download_path"
[ "$(stat -c '%s' "$download_path")" = "$expected_size" ] || { echo "Agent artifact size verification failed" >&2; exit 1; }
printf '%s  %s\n' "$expected_sha256" "$download_path" | sha256sum --check --status || { echo "Agent artifact SHA-256 verification failed" >&2; exit 1; }
chmod 0755 "$download_path"
if ! id net-probe >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin net-probe
fi
chmod 0755 "$staging_dir"
panel_url="${NET_PROBE_PANEL_URL:-}"
ca_fingerprint="${NET_PROBE_CA_FINGERPRINT:-}"
enrollment_code="${NET_PROBE_ENROLLMENT_CODE:-}"
node_id="${NET_PROBE_NODE_ID:-$(hostname)}"
python3 - "$panel_url" "$ca_fingerprint" "$enrollment_code" "$node_id" <<'PY'
import re, sys, urllib.parse
panel_url, fingerprint, code, node_id = sys.argv[1:]
url = urllib.parse.urlsplit(panel_url)
if url.scheme != "https" or not url.hostname or url.username or url.password or url.query or url.fragment:
    raise SystemExit("NET_PROBE_PANEL_URL must be a secure HTTPS origin")
if not re.fullmatch(r"[0-9a-f]{64}", fingerprint):
    raise SystemExit("NET_PROBE_CA_FINGERPRINT must be 64 lowercase hexadecimal characters")
if not code or len(code) > 4096:
    raise SystemExit("NET_PROBE_ENROLLMENT_CODE is required")
if not node_id or len(node_id) > 128 or any(ch in node_id for ch in "\r\n\x00"):
    raise SystemExit("NET_PROBE_NODE_ID is invalid")
PY
for required_command in runuser hostname; do
  command -v "$required_command" >/dev/null 2>&1 || { echo "missing required command: $required_command" >&2; exit 1; }
done

stage_config_home="$staging_dir/config"
stage_state_home="$staging_dir/state"
stage_agent_dir="$stage_config_home/net-probe"
stage_pki_dir="$stage_agent_dir/pki"
install -d -o net-probe -g net-probe -m 0700 "$stage_config_home" "$stage_state_home" "$stage_agent_dir" "$stage_pki_dir"
render_config() {
  python3 - "$1" "$2" "$panel_url" "$node_id" <<'PY'
import json, pathlib, sys
target, pki_dir, panel_url, node_id = sys.argv[1:]
q = json.dumps
body = f'''[agent]
node_id = {q(node_id)}
log_level = "info"
report_interval = "60s"
collect_timeout = "45s"
shutdown_timeout = "20s"

[panel]
url = {q(panel_url)}
ca_file = {q(pki_dir + "/ca.crt")}
cert_file = {q(pki_dir + "/agent.crt")}
key_file = {q(pki_dir + "/agent.key")}
command_key_file = {q(pki_dir + "/command-signing.pub")}
release_key_file = {q(pki_dir + "/release-signing.pub")}

[collect]
disk_mounts = ["/"]
upgradable = true
'''
pathlib.Path(target).write_text(body)
PY
}
render_config "$stage_agent_dir/config.toml" "$stage_pki_dir"
chown net-probe:net-probe "$stage_agent_dir/config.toml"
chmod 0600 "$stage_agent_dir/config.toml"
printf '%s\n' "$enrollment_code" | runuser -u net-probe -- env XDG_CONFIG_HOME="$stage_config_home" XDG_STATE_HOME="$stage_state_home" \
  "$download_path" enroll --panel-url "$panel_url" --ca-fingerprint "$ca_fingerprint" --code-stdin >/dev/null
runuser -u net-probe -- env XDG_CONFIG_HOME="$stage_config_home" XDG_STATE_HOME="$stage_state_home" \
  "$download_path" --config "$stage_agent_dir/config.toml" --once >/dev/null

version_dir="/opt/net-probe/versions/${resolved_version}"
install -d -o root -g root -m 0755 /opt/net-probe/versions "$version_dir"
install -o root -g root -m 0755 "$download_path" "$version_dir/net-probe"
install -d -o root -g root -m 0755 /etc/net-probe /etc/net-probe/services.d /etc/net-probe/trust
install -d -o net-probe -g net-probe -m 0700 /etc/net-probe/pki
for identity_file in agent.key agent.crt ca.crt command-signing.pub release-signing.pub identity.json; do
  install -o net-probe -g net-probe -m 0600 "$stage_pki_dir/$identity_file" "/etc/net-probe/pki/$identity_file"
done
install -o root -g root -m 0644 "$stage_pki_dir/command-signing.pub" /etc/net-probe/trust/command-signing.pub
render_config "$staging_dir/config.toml" /etc/net-probe/pki
install -o net-probe -g net-probe -m 0600 "$staging_dir/config.toml" /etc/net-probe/config.toml
install -d -o root -g net-probe -m 0770 /var/lib/net-probe-updates
ln -sfn "$version_dir/net-probe" /usr/local/bin/net-probe

cat > /etc/systemd/system/net-probe.service <<'EOF'
[Unit]
Description=net-probe agent
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
NotifyAccess=main
User=net-probe
Group=net-probe
ExecStart=/usr/local/bin/net-probe --config /etc/net-probe/config.toml
Restart=on-failure
RestartSec=5s
WatchdogSec=90s
TimeoutStartSec=30s
StateDirectory=net-probe
StateDirectoryMode=0700
RuntimeDirectory=net-probe
RuntimeDirectoryMode=0750
Environment=NET_PROBE_UPDATE_DIRECTORY=/var/lib/net-probe-updates
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ReadWritePaths=/etc/net-probe/pki /var/lib/net-probe-updates

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/net-probe-update.path <<'EOF'
[Unit]
Description=Watch for a verified net-probe Agent update request

[Path]
PathExists=/var/lib/net-probe-updates/pending.json
PathExists=/var/lib/net-probe-updates/claimed.json
Unit=net-probe-update.service

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/net-probe-update.service <<'EOF'
[Unit]
Description=Install and prove a verified net-probe Agent update
After=net-probe.service

[Service]
Type=oneshot
User=root
Group=root
ExecStart=/usr/local/bin/net-probe internal-update-helper
UMask=0077
NoNewPrivileges=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectSystem=strict
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX
IPAddressDeny=any
ReadWritePaths=/var/lib/net-probe-updates /opt/net-probe/versions /usr/local/bin /run/net-probe
EOF

systemctl daemon-reload
systemctl disable --now net-probe.timer >/dev/null 2>&1 || true
rm -f /etc/systemd/system/net-probe.timer
systemctl enable --now net-probe.service
systemctl enable --now net-probe-update.path
for _attempt in $(seq 1 30); do
  if systemctl is-active --quiet net-probe.service; then
    break
  fi
  sleep 1
done
systemctl is-active --quiet net-probe.service || { echo "net-probe Agent did not become ready" >&2; exit 1; }

echo "installed net-probe ${resolved_version} for ${arch}"
echo "config: /etc/net-probe/config.toml"
echo "reload after config changes: systemctl restart net-probe.service"
