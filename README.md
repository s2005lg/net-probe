# net-probe

`net-probe` 是面向 Linux VPS 的轻量监控与服务探测系统，由 Panel 和常驻 Agent 组成。Agent 采集主机健康、出口 IP/国家地区、服务状态及可用遥测，并通过 mTLS WebSocket 与 Panel 保持出站控制连接。

项目采用 [MIT License](LICENSE)。

## 能力概览

- 常驻 `systemd` Agent：周期采集、断线重连、即时采集、配置重载和自检。
- 安全注册：Panel CA 指纹固定、10 分钟内有效的一次性注册码、每个 Agent 独立的 90 天客户端证书。
- 控制通道：仅出站 WSS、mTLS、Ed25519 命令签名、单调序列号、防重放、命令 TTL 和审计历史。
- 安全升级：签名 Release 导入、管理员重新认证、输入确认、按节点/批量升级、就绪证明及自动回滚。
- 主机指标：负载、内存、磁盘、运行时间、可升级软件包和公网出口 IPv4/IPv6。
- 服务识别：Hysteria2、Xray、V2Ray、sing-box、Shadowsocks、Trojan、TUIC、AnyTLS。
- 协议标签：VLESS 是 Xray/sing-box 的协议标签，不是独立守护进程。

不同服务能提供的指标并不相同。Panel 按每项能力展示 `supported`、`unsupported` 或 `unknown`，并区分未配置、已禁用、采集失败和真实的零值，不会把 AnyTLS 等无法原生提供的流量或连接数显示成 0。

## 安装

当前 Agent 使用 control-v1，不保证兼容旧 Panel。必须先安装同一 Release 的 Panel，再从 Panel 生成 Agent 安装命令。

### 1. 安装 Panel

Panel 必须有一个 Agent 可访问、证书覆盖的 HTTPS 公网地址：

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install-panel.sh | \
  sudo NET_PROBE_PANEL_VERSION=v0.1.0 \
       NET_PROBE_PANEL_PUBLIC_URL="https://panel.example.com:24443" \
       NET_PROBE_PANEL_PORT=24443 bash
```

可选设置 `NET_PROBE_PANEL_ADMIN_PASSWORD`；不设置时安装器会生成并打印。安装结果包括：

- `/usr/local/bin/net-probe-panel`
- `/etc/net-probe-panel/config.toml`
- `/etc/net-probe-panel/panel.env`（`0600`）
- `net-probe-panel.service`
- Panel CA SHA-256 指纹

打开 Panel 地址，以 `admin` 登录。

### 2. 在 Panel 创建 Agent 注册命令

进入“探针管理”，创建一次性注册码。注册码最长 10 分钟有效且只能消费一次。Panel 会生成包含以下固定值的完整命令：

- 明确的 Release 版本，不接受 `latest`
- Panel HTTPS 地址
- Panel CA 指纹
- 一次性注册码
- Release Ed25519 公钥

把 Panel 显示的命令原样复制到目标 VPS 执行。等价形式如下：

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh | \
  sudo NET_PROBE_VERSION="vX.Y.Z" \
       NET_PROBE_PANEL_URL="https://panel.example.com:24443" \
       NET_PROBE_CA_FINGERPRINT="<64位小写十六进制指纹>" \
       NET_PROBE_ENROLLMENT_CODE="<一次性注册码>" \
       NET_PROBE_RELEASE_PUBLIC_KEY_HEX="<64位小写十六进制公钥>" bash
```

安装器会先下载并验证 Agent 与 root update helper 的签名清单、大小和 SHA-256，再在临时目录完成注册、首次上报和 mTLS WSS hello/welcome 预检。只有预检成功后才切换生产文件；失败不会替换旧部署。

安装结果包括：

- `/usr/local/bin/net-probe` → `/opt/net-probe/versions/<version>/net-probe`
- `/usr/local/libexec/net-probe-update-helper`（root-owned）
- `/etc/net-probe/config.toml`
- `/etc/net-probe/pki/`（Agent 身份与固定信任材料）
- `net-probe.service`
- `net-probe-update.path` 与受限的 root helper service

旧的 `net-probe.timer` 会被停用并移除。

## 常驻运行与状态

```bash
sudo systemctl status net-probe.service
sudo journalctl -u net-probe.service --since "10 minutes ago"
sudo systemctl restart net-probe.service
```

Agent 根据 `report_interval` 周期采集，同时维持控制连接。Panel 中常见状态：

- 在线：最近心跳在离线阈值内。
- 离线：超过阈值未收到心跳。
- 已撤销：证书身份被管理员撤销，必须重新注册。
- 证书临近到期：Agent 会在到期前通过 mTLS 自动续期；身份文件损坏、证书过期或被撤销时需重新创建一次性注册码。

配置修改后可在 Panel 下发“重载配置”，也可以执行：

```bash
sudo systemctl kill -s HUP net-probe.service
```

## Panel 操作权限

- `viewer`：查看节点、指标、Release 和命令历史。
- `operator`：包含查看权限，可下发即时采集、重载配置和自检。
- `admin`：节点、标签、设置、注册、吊销和升级管理。

升级属于高风险操作，仅 `admin` 可执行。批量升级前必须再次输入当前密码完成短时重新认证，并输入 Panel 要求的确认文本。即时采集 TTL 为 5 分钟，配置重载和自检为 30 分钟，升级为 24 小时；到期命令不会再派发。Agent 会按序执行并持久化去重状态。

## 签名升级与回滚

1. 在“探针管理”导入 GitHub Release 的 Agent manifest 与签名。
2. 确认版本、架构、哈希、有效期和最小 Panel 版本。
3. 重新认证并选择目标 Agent。
4. 查看命令历史中的 `queued`、`dispatched`、`accepted`、`running`、`succeeded`、`failed` 或 `expired`。

Agent 下载后再次验证 Release 签名、平台、版本、URL、大小和哈希。root helper 还会独立验证 Panel 命令签名及 root-owned 信任材料，然后原子切换版本并重启。新进程必须在 120 秒内提交与 Agent、命令、版本及当前 boot ID 绑定的就绪证明，否则自动切回上一版本。服务器上只保留当前版和上一版 Agent，失败原因与回滚状态可在命令历史查看。

## 最小配置

安装器生成的 `/etc/net-probe/config.toml` 类似：

```toml
[agent]
node_id = "edge-01"
log_level = "info"
report_interval = "60s"
collect_timeout = "45s"
shutdown_timeout = "20s"

[panel]
url = "https://panel.example.com:24443"
ca_file = "/etc/net-probe/pki/ca.crt"
cert_file = "/etc/net-probe/pki/agent.crt"
key_file = "/etc/net-probe/pki/agent.key"
command_key_file = "/etc/net-probe/pki/command-signing.pub"
release_key_file = "/etc/net-probe/pki/release-signing.pub"

[collect]
disk_mounts = ["/"]
upgradable = true
```

检查配置并预览报告：

```bash
sudo net-probe --check --config /etc/net-probe/config.toml
```

执行一次采集并退出：

```bash
sudo -u net-probe net-probe --once --config /etc/net-probe/config.toml
```

## 出口 IP 与地理位置

Agent 自动探测公网出口 IPv4/IPv6，缓存成功结果并随报告上送。Panel 使用持久化缓存补充国家/地区，外部提供方失败不会阻断主报告，也不会覆盖上一次成功位置。可在 Panel 配置中调整：

```toml
[geo]
refresh_interval = "12h"
provider = "ipwhois"
url = "https://ipwho.is/{ip}?lang=zh-CN"
timeout = "4s"
token_env = ""
```

## 服务遥测

内置模板位于 `internal/detect/builtin/`。服务实现与协议标签分离，例如 VLESS 由 Xray 或 sing-box 提供。流量和在线连接数是服务实例聚合值，不承诺是某个 inbound 或用户的独立值。

可禁用某类服务的遥测采集而不隐藏服务发现结果：

```toml
[stats.services.anytls]
enabled = false
```

自定义模板放在 `/etc/net-probe/services.d/*.yaml`，然后向 Agent 发送配置重载或 `SIGHUP`。

## 网络与安全边界

Agent 不需要任何入站端口。防火墙只需允许：

- Agent → Panel HTTPS/WSS 端口（TCP）。
- Agent → GitHub Releases 的 HTTPS（TCP 443，仅升级时）。
- Agent → 配置的出口 IP 查询服务和系统 DNS/NTP。

Panel 只需开放其 HTTPS 端口给管理员和 Agent。建议在云防火墙限制来源，并使用域名和受信证书；若使用自签 CA，Agent 依靠安装时固定的 CA 指纹建立初始信任，不提供 `skip-cert-verify` 开关。

Agent 主进程以无登录权限的 `net-probe` 用户运行，并启用 systemd 文件系统与设备隔离。只有独立 helper 以 root 运行，且无网络访问，只能处理固定目录中的请求、切换固定 Agent 软链接和重启服务。

## 无敏感信息排障

不要在工单、截图或日志中粘贴注册码、私钥、管理员密码或完整安装命令。安全检查顺序：

```bash
sudo systemctl status net-probe.service --no-pager
sudo journalctl -u net-probe.service -n 100 --no-pager
sudo stat -c '%U:%G %a %n' /etc/net-probe/config.toml /etc/net-probe/pki/*
sudo readlink /usr/local/bin/net-probe
```

分享日志前删除 URL 中的内部主机名/IP、Agent ID、命令 ID 和证书序列号。注册码一旦泄露应立即丢弃并重新生成；Agent 私钥疑似泄露时应先在 Panel 吊销，再重新注册。

## 卸载

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/uninstall.sh | sudo bash
```

卸载前如需保留审计和历史指标，请先备份 Panel 的 `/var/lib/net-probe-panel`。
