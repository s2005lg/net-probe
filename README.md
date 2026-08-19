# net-probe

`net-probe` is a small Linux agent that periodically collects host health and
detects installed proxy/VPN server processes, then uploads a JSON report to a
panel or webhook sink.

## Features

- Runs as a periodic `systemd` timer (no long-lived daemon).
- Detects services by matching systemd units and running binaries against
  built-in and custom YAML templates.
- Collects hostname, OS/kernel metadata, load, memory, disk usage, uptime, and
  available package upgrades.
- Reports listening ports and certificate expiry for detected services.
- Sends reports over HTTPS to `panel` or `webhook` sinks with optional bearer
  token authentication.

## Recommended installation order

The panel and agents share a single agent token: the panel requires it to
accept reports, and every agent must present the same value when it uploads.
Install the panel first, then the agents.

1. Install the panel and note the `agent token` it prints (or pin your own):

   ```bash
   curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install-panel.sh | sudo bash
   # output includes: agent token: <token>
   ```

2. Install the agent on each node with the panel URL and the same token:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh | \
     sudo NET_PROBE_PANEL_URL="https://<panel-ip>:<port>" \
          NET_PROBE_PANEL_TOKEN="<same-token>" bash
   ```

If you omit the token:

- Panel: the installer generates a random token and prints it — copy that value
  to every agent.
- Agent: the installer configures the panel sink without a token, so the panel
  rejects reports with `401 Unauthorized`. Always pass `NET_PROBE_PANEL_TOKEN`
  matching the panel's token.

## One-line install

Install the latest release directly from GitHub:

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh | sudo bash
```

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh | \
  sudo NET_PROBE_VERSION=v0.1.0 bash
```

Configure a panel sink during installation (optional). The token is written to
`/etc/net-probe/panel-token` with mode `0600`:

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh | \
  sudo NET_PROBE_PANEL_URL="https://panel.example.com" \
       NET_PROBE_PANEL_TOKEN="your-token" bash
```

If no panel URL is provided, the installer writes a placeholder webhook config
to `/etc/net-probe/config.toml` for you to edit.

The installer:

- writes the binary to `/usr/local/bin/net-probe`
- creates the `net-probe` system user
- writes the config to `/etc/net-probe/config.toml`
- installs and enables the `net-probe.timer` systemd unit
- runs the agent every 1 minute by default

## One-line uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/uninstall.sh | sudo bash
```

This stops and disables the timer, removes the binary, config directory,
systemd units, and the `net-probe` system user, leaving no residue.

## Install the panel

The `net-probe-panel` is a single binary with the web UI embedded. It listens
on a random port in the range 20000–65535 (override with
`NET_PROBE_PANEL_PORT`) with a self-signed certificate and stores data in
SQLite:

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install-panel.sh | sudo bash
```

The installer generates a random port, agent token, and admin password and
prints all three at the end. To pin a version or preset them:

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install-panel.sh | \
  sudo NET_PROBE_PANEL_VERSION=v0.2.0 \
       NET_PROBE_PANEL_PORT=24443 \
       NET_PROBE_PANEL_AGENT_TOKEN="agent-token" \
       NET_PROBE_PANEL_ADMIN_PASSWORD="admin-password" bash
```

To generate the agent token yourself instead of letting the installer pick a
random one, use `openssl`:

```bash
openssl rand -hex 24
```

This prints a 48-character hex string. Use that exact value for
`NET_PROBE_PANEL_AGENT_TOKEN` when installing the panel and for the panel sink
token on the agent (for example `NET_PROBE_PANEL_TOKEN` in
`/etc/net-probe/config.toml`); if they do not match, the panel rejects agent
reports with `401 Unauthorized`. The token is stored in
`/etc/net-probe-panel/config.toml` as `[agent].token` and must be kept secret.

The panel:

- writes the binary to `/usr/local/bin/net-probe-panel`
- creates the `net-probe-panel` system user
- writes config to `/etc/net-probe-panel/config.toml` and the admin password
  to `/etc/net-probe-panel/panel.env` (mode `0600`)
- installs and enables the `net-probe-panel` systemd service
- listens on the chosen port and generates a long-lived self-signed certificate
  on first start

Open `https://<panel-ip>:<port>` in a browser (accept the self-signed
certificate) and log in with the admin password. Agent sinks must set
`tls_skip_verify = true` (or point `tls_ca_file` at the panel cert) because the
certificate is self-signed.

## Minimal configuration

Edit `/etc/net-probe/config.toml`:

```toml
[agent]
node_id = "edge-01"
log_level = "info"

[[sink]]
type = "panel"
url = "https://panel.example.com"
token_env = "NET_PROBE_PANEL_TOKEN"

[collect]
disk_mounts = ["/"]
upgradable = true

[detect]
include = ["hysteria2", "xray", "v2ray", "sing-box", "shadowsocks", "trojan", "tuic", "anytls"]
custom_dir = "/etc/net-probe/services.d"
```

Provide the token in the environment when the timer runs, for example in a
systemd drop-in or the shell that runs `net-probe`.

## Validate configuration and preview a report

Run a dry-run that validates the config, collects the current host and service
data, and prints a JSON report preview without sending it:

```bash
sudo net-probe --check --config /etc/net-probe/config.toml
```

To print the version:

```bash
net-probe --version
```

## Supported services

The built-in detection templates are:

- Hysteria2
- Xray
- V2Ray
- sing-box
- Shadowsocks
- Trojan
- TUIC
- AnyTLS

See `internal/detect/builtin/*.yaml` for the built-in patterns. Custom
templates are loaded from `/etc/net-probe/services.d` by default.

## Security notes

- The agent runs as root so it can read `/proc/<pid>/fd` of other processes
  (needed to report listening ports); the systemd unit still sets
  `NoNewPrivileges=true` and `ProtectSystem=strict`.
- Panel sinks require HTTPS unless `insecure_allow_http = true` is explicitly
  set for localhost-only or otherwise trusted endpoints.
- Secrets are not embedded in the report or config; use `token_env` or
  `token_file` so the token is read from an environment variable or file.

## Writing a detection template

Create a YAML file in `/etc/net-probe/services.d/`, for example
`my-service.yaml`:

```yaml
id: my-service
name: My Service
units: ["my-service", "my-service@.*"]
binary_patterns: ["my-service"]
version_cmd: ["--version"]
transport: ["tcp"]
cert_paths: []
listen_ports: []
stats_kind: ""
```

Template fields:

- `id`: stable identifier used in the report `type` field.
- `name`: human-readable service name.
- `units`: regular expressions anchored to systemd unit names (without the
  `.service` suffix).
- `binary_patterns`: regular expressions matched against the service's
  `ExecStart` path.
- `version_cmd`: arguments used to query the binary version.
- `transport`: informational transport list, such as `tcp` or `udp`.
- `cert_paths`: optional paths checked for TLS certificate expiry.
- `listen_ports`: optional ports that must be present for `listen_ok` to be
  true.
- `stats_kind`: optional service-specific statistics kind identifier.

After adding or editing templates, restart the timer so the next run picks
them up:

```bash
sudo systemctl restart net-probe.timer
```

---

## 中文说明

`net-probe` 是一个轻量级 Linux 探针，会定时采集主机健康信息，识别已安装的代理 / VPN 服务进程，并把 JSON 报告上传到 panel 或 webhook。

### 功能特性

- 通过 `systemd` timer 定时运行，不使用常驻守护进程。
- 通过内置或自定义 YAML 模板，匹配 systemd 单元和运行中的二进制文件来识别服务。
- 采集主机名、操作系统 / 内核信息、负载、内存、磁盘使用率、运行时间以及可升级软件包数量。
- 报告已识别服务的监听端口和证书到期时间。
- 通过 HTTPS 向 `panel` 或 `webhook` 上报，支持可选的 Bearer Token 认证。

### 推荐安装顺序

Panel 和 Agent 共享同一个 Agent Token：Panel 用它校验上报，每个 Agent 上报时都必须携带同一个值。建议先安装 Panel，再安装 Agent。

1. 先安装 Panel，并记录它最后打印的 `agent token`（也可以自己指定）：

   ```bash
   curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install-panel.sh | sudo bash
   # 输出中会包含：agent token: <token>
   ```

2. 再在每个节点安装 Agent，传入 Panel 地址和同一个 Token：

   ```bash
   curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh |   sudo NET_PROBE_PANEL_URL="https://<panel-ip>:<port>"        NET_PROBE_PANEL_TOKEN="<同一个token>" bash
   ```

如果省略 Token：

- Panel 侧：安装脚本会随机生成一个 Token 并打印，需要把这个值复制给所有 Agent。
- Agent 侧：安装脚本会生成不带 Token 的 panel sink，Panel 会以 `401 Unauthorized` 拒绝上报。因此 Agent 侧务必传入与 Panel 一致的 `NET_PROBE_PANEL_TOKEN`。

### 一键安装

直接从 GitHub 安装最新版本：

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh | sudo bash
```

安装指定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh |   sudo NET_PROBE_VERSION=v0.1.0 bash
```

安装时配置 panel（可选）。Token 会写入 `/etc/net-probe/panel-token`，文件权限为 `0600`：

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install.sh |   sudo NET_PROBE_PANEL_URL="https://panel.example.com"        NET_PROBE_PANEL_TOKEN="your-token" bash
```

如果没有提供 panel URL，安装脚本会生成一个占位的 webhook 配置到 `/etc/net-probe/config.toml`，由你自行编辑。

安装脚本会：

- 将二进制文件写入 `/usr/local/bin/net-probe`
- 创建 `net-probe` 系统用户
- 将配置写入 `/etc/net-probe/config.toml`
- 安装并启用 `net-probe.timer` systemd 单元
- 默认每 1 分钟运行一次探针

### 一键卸载

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/uninstall.sh | sudo bash
```

卸载脚本会停止并禁用 timer，删除二进制文件、配置目录、systemd 单元和 `net-probe` 系统用户，不留下残留。

### 安装 panel

`net-probe-panel` 是一个内置 Web UI 的单文件二进制程序。它默认监听 20000–65535 范围内的随机端口，可通过 `NET_PROBE_PANEL_PORT` 覆盖，使用自签名证书，并将数据存储到 SQLite：

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install-panel.sh | sudo bash
```

安装脚本会随机生成端口、Agent Token 和管理员密码，并在最后打印这三项信息。如需固定版本或预先指定：

```bash
curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/install-panel.sh |   sudo NET_PROBE_PANEL_VERSION=v0.2.0        NET_PROBE_PANEL_PORT=24443        NET_PROBE_PANEL_AGENT_TOKEN="agent-token"        NET_PROBE_PANEL_ADMIN_PASSWORD="admin-password" bash
```

如需自己生成 Agent Token，而不是让安装脚本随机生成，可以使用 `openssl`：

```bash
openssl rand -hex 24
```

这会生成一个 48 位十六进制字符串。安装 panel 时，将该值作为 `NET_PROBE_PANEL_AGENT_TOKEN` 传入；在 Agent 侧，把同一个值设置为 panel sink 的 Token（例如 `/etc/net-probe/config.toml` 中的 `NET_PROBE_PANEL_TOKEN`）。两者不一致时，panel 会以 `401 Unauthorized` 拒绝 Agent 上报。Token 会保存在 `/etc/net-probe-panel/config.toml` 的 `[agent].token` 中，请妥善保管。

panel 会：

- 将二进制文件写入 `/usr/local/bin/net-probe-panel`
- 创建 `net-probe-panel` 系统用户
- 将配置写入 `/etc/net-probe-panel/config.toml`，并将管理员密码写入 `/etc/net-probe-panel/panel.env`（权限 `0600`）
- 安装并启用 `net-probe-panel` systemd 服务
- 监听所选端口，并在首次启动时生成长期有效的自签名证书

在浏览器中打开 `https://<panel-ip>:<port>`，接受自签名证书，然后使用管理员密码登录。由于证书是自签名的，Agent 上报时需要设置 `tls_skip_verify = true`，或将 `tls_ca_file` 指向 panel 证书。

### 最小配置示例

编辑 `/etc/net-probe/config.toml`：

```toml
[agent]
node_id = "edge-01"
log_level = "info"

[[sink]]
type = "panel"
url = "https://panel.example.com"
token_env = "NET_PROBE_PANEL_TOKEN"

[collect]
disk_mounts = ["/"]
upgradable = true

[detect]
include = ["hysteria2", "xray", "v2ray", "sing-box", "shadowsocks", "trojan", "tuic", "anytls"]
custom_dir = "/etc/net-probe/services.d"
```

在 timer 运行时通过环境变量提供 Token，例如在 systemd drop-in 文件或运行 `net-probe` 的 shell 中设置。

### 校验配置并预览报告

执行 dry-run，校验配置、采集当前主机和服务数据，并打印 JSON 报告预览，不会真正上报：

```bash
sudo net-probe --check --config /etc/net-probe/config.toml
```

查看版本：

```bash
net-probe --version
```

### 支持的服务

内置检测模板包括：

- Hysteria2
- Xray
- V2Ray
- sing-box
- Shadowsocks
- Trojan
- TUIC
- AnyTLS

内置匹配规则位于 `internal/detect/builtin/*.yaml`。默认从 `/etc/net-probe/services.d` 加载自定义模板。

### 安全说明

- Agent 以 root 身份运行，以便读取其他进程的 `/proc/<pid>/fd`（报告监听端口所需）；systemd 单元仍然设置了 `NoNewPrivileges=true` 和 `ProtectSystem=strict`。
- panel 上报默认要求 HTTPS，除非针对仅限本机或其他可信端点显式设置 `insecure_allow_http = true`。
- 密钥不会写入报告或配置文件；请使用 `token_env` 或 `token_file`，让 Token 从环境变量或文件中读取。

### 编写检测模板

在 `/etc/net-probe/services.d/` 中创建 YAML 文件，例如 `my-service.yaml`：

```yaml
id: my-service
name: My Service
units: ["my-service", "my-service@.*"]
binary_patterns: ["my-service"]
version_cmd: ["--version"]
transport: ["tcp"]
cert_paths: []
listen_ports: []
stats_kind: ""
```

模板字段说明：

- `id`：报告中 `type` 字段使用的稳定标识。
- `name`：便于阅读的服务名称。
- `units`：匹配 systemd 单元名称的正则表达式，不包含 `.service` 后缀。
- `binary_patterns`：匹配服务 `ExecStart` 路径的正则表达式。
- `version_cmd`：查询二进制版本时使用的参数。
- `transport`：信息性的传输方式列表，例如 `tcp` 或 `udp`。
- `cert_paths`：可选，用于检查 TLS 证书到期时间的路径。
- `listen_ports`：可选，`listen_ok` 为 true 时必须存在的端口列表。
- `stats_kind`：可选，服务专用的统计类型标识。

添加或修改模板后，重启 timer 让下一次运行生效：

```bash
sudo systemctl restart net-probe.timer
```
