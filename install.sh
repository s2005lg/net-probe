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

version="${NET_PROBE_VERSION:-latest}"
if [ "$version" = "latest" ]; then
  base="https://github.com/s2005lg/net-probe/releases/latest/download"
else
  base="https://github.com/s2005lg/net-probe/releases/download/${version}"
fi

curl -fsSL "${base}/net-probe_linux_${arch}" -o /usr/local/bin/net-probe
chmod 755 /usr/local/bin/net-probe

if ! id net-probe >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin net-probe
fi

install -d -m 0755 /etc/net-probe/services.d

if [ -n "${NET_PROBE_PANEL_URL:-}" ]; then
  {
    echo '[[sink]]'
    echo 'type = "panel"'
    echo "url = \"${NET_PROBE_PANEL_URL}\""
    if [ -n "${NET_PROBE_PANEL_TOKEN:-}" ]; then
      install -d -m 0755 /etc/net-probe
      printf '%s\n' "${NET_PROBE_PANEL_TOKEN}" > /etc/net-probe/panel-token
      chmod 600 /etc/net-probe/panel-token
      echo 'token_file = "/etc/net-probe/panel-token"'
    fi
  } > /etc/net-probe/config.toml
  chmod 600 /etc/net-probe/config.toml
else
  cat > /etc/net-probe/config.toml <<'EOF'
[[sink]]
type = "webhook"
url = "https://example.com/net-probe-report"
EOF
  chmod 600 /etc/net-probe/config.toml
fi

cat > /etc/systemd/system/net-probe.service <<'EOF'
[Unit]
Description=net-probe agent
After=network-online.target

[Service]
Type=oneshot
User=net-probe
ExecStart=/usr/local/bin/net-probe --config /etc/net-probe/config.toml
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/etc/net-probe
EOF

cat > /etc/systemd/system/net-probe.timer <<'EOF'
[Unit]
Description=Run net-probe periodically

[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
AccuracySec=5s

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now net-probe.timer

echo "installed net-probe ${version} for ${arch}"
echo "config: /etc/net-probe/config.toml"
echo "reload after config changes: systemctl restart net-probe.timer"
