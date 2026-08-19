#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "run as root: curl -fsSL .../install-panel.sh | sudo bash" >&2
  exit 1
fi

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

version="${NET_PROBE_PANEL_VERSION:-latest}"
if [ "$version" = "latest" ]; then
  base="https://github.com/s2005lg/net-probe/releases/latest/download"
else
  base="https://github.com/s2005lg/net-probe/releases/download/${version}"
fi

if [ -n "${NET_PROBE_PANEL_PORT:-}" ]; then
  port="${NET_PROBE_PANEL_PORT}"
else
  port="$(shuf -i 20000-65535 -n 1)"
fi
if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
  echo "invalid NET_PROBE_PANEL_PORT: ${port}" >&2
  exit 1
fi

curl -fsSL "${base}/net-probe-panel_linux_${arch}" -o /usr/local/bin/net-probe-panel
chmod 755 /usr/local/bin/net-probe-panel

if ! id net-probe-panel >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin net-probe-panel
fi

install -d -m 0755 /etc/net-probe-panel
install -d -m 0755 /var/lib/net-probe-panel
chown net-probe-panel:net-probe-panel /var/lib/net-probe-panel

agent_token="${NET_PROBE_PANEL_AGENT_TOKEN:-$(openssl rand -hex 24)}"
admin_password="${NET_PROBE_PANEL_ADMIN_PASSWORD:-$(openssl rand -hex 24)}"

cat > /etc/net-probe-panel/config.toml <<EOF
listen_addr = ":${port}"
data_dir = "/var/lib/net-probe-panel"
node_timeout = "3m"

[agent]
token = "${agent_token}"

[admin]
user = "admin"

[retention]
raw_days = 7
hourly_days = 30
daily_days = 365
EOF
chmod 600 /etc/net-probe-panel/config.toml
chown net-probe-panel:net-probe-panel /etc/net-probe-panel/config.toml

printf 'NET_PROBE_PANEL_ADMIN_PASSWORD=%s\n' "${admin_password}" > /etc/net-probe-panel/panel.env
chmod 600 /etc/net-probe-panel/panel.env

cat > /etc/systemd/system/net-probe-panel.service <<'EOF'
[Unit]
Description=net-probe-panel
After=network-online.target

[Service]
Type=simple
User=net-probe-panel
EnvironmentFile=-/etc/net-probe-panel/panel.env
ExecStart=/usr/local/bin/net-probe-panel --config /etc/net-probe-panel/config.toml
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/net-probe-panel /etc/net-probe-panel

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now net-probe-panel.service

# Resolve a public URL for the agent install one-liner.
panel_url="${NET_PROBE_PANEL_PUBLIC_URL:-}"
addr_note=""
if [ -z "$panel_url" ]; then
  panel_ip=""
  if command -v curl >/dev/null 2>&1; then
    panel_ip="$(curl -fsSL -m 5 https://api.ipify.org 2>/dev/null || curl -fsSL -m 5 https://ifconfig.me 2>/dev/null || true)"
  fi
  if [ -z "$panel_ip" ]; then
    panel_ip="$(hostname -I 2>/dev/null | tr ' ' '\n' | awk '/^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ {print; exit}' || true)"
  fi
  if [ -n "$panel_ip" ]; then
    panel_url="https://${panel_ip}:${port}"
    case "$panel_ip" in
      10.*|127.*|192.168.*|172.1[6-9].*|172.2[0-9].*|172.3[0-1].*|::1|fe80:*|fc*|fd*)
        addr_note="note: detected address may be private/NAT-local; replace it with the panel's public IP or domain."
        ;;
    esac
  else
    panel_url="https://<panel-ip>:${port}"
    addr_note="note: could not detect the panel address; replace <panel-ip> with the panel's public IP or domain."
  fi
fi

echo "installed net-probe-panel ${version} for ${arch}"
echo "config: /etc/net-probe-panel/config.toml"
echo "listen port: ${port}"
echo "agent token: ${agent_token}"
echo "admin password: ${admin_password}"
echo
echo "===== agent install command (copy to each agent node) ====="
echo "curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh | \\"
echo "  sudo NET_PROBE_PANEL_URL=\"${panel_url}\" \\"
echo "       NET_PROBE_PANEL_TOKEN=\"${agent_token}\" bash"
echo "==========================================================="
if [ -n "$addr_note" ]; then
  echo "$addr_note"
fi
