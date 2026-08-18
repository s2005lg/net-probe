#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "run as root: curl -fsSL .../uninstall.sh | sudo bash" >&2
  exit 1
fi

MARKER="/etc/net-probe/.net-probe-installed"
BIN="/usr/local/bin/net-probe"
CONFIG_DIR="/etc/net-probe"
MARKER_EXISTED=0
[ -e "$MARKER" ] && MARKER_EXISTED=1

# 停止并禁用 timer（会同时移除 timers.target.wants 下的软链接）。
systemctl disable --now net-probe.timer >/dev/null 2>&1 || true
systemctl reset-failed net-probe.service net-probe.timer >/dev/null 2>&1 || true

# 删除 systemd 单元和可能的残留软链接。
rm -f \
  /etc/systemd/system/net-probe.service \
  /etc/systemd/system/net-probe.timer \
  /etc/systemd/system/timers.target.wants/net-probe.timer

systemctl daemon-reload

# 删除二进制。
rm -f "$BIN"

# 删除配置目录。
if [ "$MARKER_EXISTED" -eq 1 ]; then
  rm -rf "$CONFIG_DIR"
else
  # 没有安装标记时也尽力清理已知文件，但保留未由本工具创建的目录。
  rm -f "$CONFIG_DIR/config.toml"
  rm -rf "$CONFIG_DIR/services.d"
  rm -f "$CONFIG_DIR/panel-token"
fi

# 仅当确为本工具创建的系统用户时才删除（避免误删管理员已有的同名用户）。
if [ "$MARKER_EXISTED" -eq 1 ] && id net-probe >/dev/null 2>&1; then
  if getent passwd net-probe | cut -d: -f7 | grep -q '/nologin'; then
    userdel net-probe
  fi
fi

echo "net-probe uninstalled"
