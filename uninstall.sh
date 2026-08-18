#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "run as root: curl -fsSL .../uninstall.sh | sudo bash" >&2
  exit 1
fi

BIN="/usr/local/bin/net-probe"
CONFIG_DIR="/etc/net-probe"

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
rm -rf "$CONFIG_DIR"

# 仅当确为本工具创建的系统用户时才删除（避免误删管理员已有的同名用户）。
if id net-probe >/dev/null 2>&1; then
  shell="$(getent passwd net-probe | cut -d: -f7)"
  home="$(getent passwd net-probe | cut -d: -f6)"
  if [ "$shell" = "/usr/sbin/nologin" ] || [ "$shell" = "/bin/false" ]; then
    if [ "$home" = "/home/net-probe" ] || [ "$home" = "/nonexistent" ] || [ -z "$home" ]; then
      userdel net-probe
    fi
  fi
fi

echo "net-probe uninstalled"
