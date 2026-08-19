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

# Resolve panel URL and token.
# Order: environment variables -> same-host panel config -> interactive prompt.
panel_url="${NET_PROBE_PANEL_URL:-}"
panel_token="${NET_PROBE_PANEL_TOKEN:-}"

# Same-host auto-discovery: read the local panel config if present.
if [ -z "$panel_url" ] && [ -r /etc/net-probe-panel/config.toml ]; then
  local_listen="$(sed -n 's/^listen_addr[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' /etc/net-probe-panel/config.toml | head -n1)"
  local_port="${local_listen##*:}"
  case "$local_port" in
    ''|*[!0-9]*) local_port="" ;;
  esac
  local_token="$(sed -n '/^\[agent\]/,/^\[/s/^token[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' /etc/net-probe-panel/config.toml | head -n1)"
  if [ -n "$local_port" ] && [ -n "$local_token" ]; then
    panel_url="https://127.0.0.1:${local_port}"
    panel_token="$local_token"
    echo "auto-detected local panel: ${panel_url}"
  fi
fi

ask() {
  local _answer=""
  if [ -t 1 ] && [ -e /dev/tty ]; then
    printf '%s' "$1" >/dev/tty 2>/dev/null || true
    IFS= read -r _answer </dev/tty 2>/dev/null || true
  fi
  printf '%s\n' "$_answer"
}

# Interactive fallback (reads from /dev/tty because `curl | bash` uses stdin).
if [ -z "$panel_url" ]; then
  panel_url="$(ask 'panel URL (https://host:port): ')"
fi
if [ -n "$panel_url" ] && [ -z "$panel_token" ]; then
  panel_token="$(ask 'panel agent token: ')"
fi

if [ -n "$panel_url" ]; then
  {
    echo '[[sink]]'
    echo 'type = "panel"'
    echo "url = \"${panel_url}\""
    if [ "${NET_PROBE_PANEL_TLS_SKIP_VERIFY:-1}" != "0" ]; then
      echo 'tls_skip_verify = true'
    fi
    if [ -n "$panel_token" ]; then
      install -d -m 0755 /etc/net-probe
      printf '%s\n' "$panel_token" > /etc/net-probe/panel-token
      chmod 600 /etc/net-probe/panel-token
      chown net-probe:net-probe /etc/net-probe/panel-token
      echo 'token_file = "/etc/net-probe/panel-token"'
    fi
  } > /etc/net-probe/config.toml
  chmod 600 /etc/net-probe/config.toml
  chown net-probe:net-probe /etc/net-probe/config.toml
  if [ -z "$panel_token" ]; then
    echo "warning: panel token not set; reports will be rejected (401) until /etc/net-probe/panel-token is configured" >&2
  fi
else
  cat > /etc/net-probe/config.toml <<'EOF'
[[sink]]
type = "webhook"
url = "https://example.com/net-probe-report"
EOF
  chmod 600 /etc/net-probe/config.toml
  chown net-probe:net-probe /etc/net-probe/config.toml
  echo "warning: no panel configured; wrote a placeholder webhook sink to /etc/net-probe/config.toml" >&2
  echo "         edit it or re-run with NET_PROBE_PANEL_URL / NET_PROBE_PANEL_TOKEN" >&2
fi

cat > /etc/systemd/system/net-probe.service <<'EOF'
[Unit]
Description=net-probe agent
After=network-online.target

[Service]
Type=oneshot
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
