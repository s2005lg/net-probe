#!/usr/bin/env bash
set -euo pipefail

arch=$(uname -m)
case "$arch" in
  x86_64) arch="amd64" ;;
  aarch64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

version="${NET_PROBE_VERSION:-latest}"
base="https://github.com/s2005lg/net-probe/releases"
url="${base}/download/${version}/net-probe_linux_${arch}"

curl -fsSL "$url" -o /usr/local/bin/net-probe
chmod 755 /usr/local/bin/net-probe
install -d -m 0755 /etc/net-probe/services.d
install -m 0644 /dev/stdin /etc/net-probe/config.toml <<'EOF'
[[sink]]
type = "panel"
url = "https://panel.example.com"
token_env = "NET_PROBE_PANEL_TOKEN"
EOF
install -m 0644 systemd/net-probe.service /etc/systemd/system/net-probe.service
install -m 0644 systemd/net-probe.timer /etc/systemd/system/net-probe.timer
systemctl daemon-reload
systemctl enable --now net-probe.timer
echo "installed. edit /etc/net-probe/config.toml and restart net-probe.timer"
