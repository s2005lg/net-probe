#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "run as root: curl -fsSL .../uninstall.sh | sudo bash" >&2
  exit 1
fi

BIN="/usr/local/bin/net-probe"
HELPER="/usr/local/libexec/net-probe-update-helper"
CONFIG_DIR="/etc/net-probe"

# 停止并禁用常驻 Agent、升级 path，以及旧版 timer。
systemctl disable --now net-probe-update.path >/dev/null 2>&1 || true
systemctl disable --now net-probe.service >/dev/null 2>&1 || true
systemctl disable --now net-probe.timer >/dev/null 2>&1 || true
systemctl reset-failed net-probe.service net-probe-update.service net-probe-update.path net-probe.timer >/dev/null 2>&1 || true

# 删除 systemd 单元和可能的残留软链接。
rm -f \
  /etc/systemd/system/net-probe.service \
  /etc/systemd/system/net-probe-update.service \
  /etc/systemd/system/net-probe-update.path \
  /etc/systemd/system/net-probe.timer \
  /etc/systemd/system/multi-user.target.wants/net-probe.service \
  /etc/systemd/system/multi-user.target.wants/net-probe-update.path \
  /etc/systemd/system/timers.target.wants/net-probe.timer

systemctl daemon-reload

# 删除二进制。
rm -f "$BIN" "$HELPER"

# 删除配置目录。
rm -rf "$CONFIG_DIR"
rm -rf /opt/net-probe /var/lib/net-probe-updates /var/lib/net-probe /run/net-probe

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
