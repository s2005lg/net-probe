# Net-probe Agent 设计文档

- 日期：2026-08-18
- 状态：已确认待审阅
- 范围：Agent（探针）子项目，暂不含 Panel 和整体分发

## 1. 背景与目标

面向自建代理/VPN 服务器的通用、轻量、开源探针。安装在各台 VPS 上，
自动发现并采集常见服务（Hysteria2、Xray、V2Ray、sing-box、Shadowsocks、
Trojan、TUIC、AnyTLS 等）的运行状态与系统指标，上报到自研 Panel，
或推送到任意 HTTP webhook（如 Uptime Kuma Push URL）。

目标：

- 最小化资源占用：默认 one-shot 运行，跑完即退；
- 对所有实现/发行版尽量兼容：自动检测服务，不要求用户预选实现；
- 可公开发布：单文件静态二进制、跨架构、一键安装；
- 为后续高级动作（重启、升级、回滚）预留协议与安全边界。

## 2. 范围与非目标

范围内（v1）：

- systemd 与原生进程场景；
- 只读监控、自动检测、上报；
- 内置 8 类服务模板 + generic 兜底；
- 两种 sink：自研 Panel、通用 HTTP webhook；
- Agent 自身的测试、构建、安装脚本和 systemd 单元。

非目标（v1）：

- Docker 容器内服务自动发现（二期，通过 `runtime` 字段预留）；
- 特权动作执行（二期，通过命令通道预留）；
- Panel 与整体分发（另立子项目）；
- 兼容 Nezha/Beszel 等第三方私有协议。

## 3. 架构与运行模型

Agent 是一个静态 Go 二进制，默认 one-shot：

```text
config → detect(服务发现) → collect(系统指标 + 监听端口) → report(组装) → sink(上报) → 退出
```

- systemd timer 或 cron 每 1–5 分钟触发一次；
- 预留 `--resident` 模式，二期秒级命令下发时启用，不重写核心；
- v1 纯出站、无监听端口，以专用非特权用户运行；
- 内部模块：配置加载、服务检测、指标采集、报告组装、输出 sink、日志/错误处理；
- 单服务检测失败只标记该服务，不影响整体上报。

## 4. 自动检测流程

检测顺序：

1. 枚举 systemd unit，按模板 `unit` 正则匹配候选；
2. 对命中 unit 取 `ActiveState`、`SubState`、`UnitFileState`、`NRestarts`、`MainPID`；
3. 从 `ExecStart` 或 PID cmdline 解析主二进制，带超时执行 `version` 命令；
4. 监听端口：解析 `/proc/net/{tcp,tcp6,udp,udp6}`，通过 inode → PID → cmdline 归属到服务，
   收集 `{proto, addr, port}`；`/proc` 不可用时回退 `ss -lntup`；
5. 证书（仅证书型服务）：按模板路径读取并计算剩余天数；
6. 统计接口（可选，默认关）：模板发现本地端点且用户提供 secret 时才采集流量/在线数。

内置服务模板（声明式 YAML）：

- hysteria2、xray、v2ray、sing-box、shadowsocks、trojan、tuic、anytls；
- generic 兜底：未匹配时至少上报 PID、端口和 cmdline；
- 用户可在 `services.d/` 放置 YAML 覆盖默认模板或新增服务，无需改代码。

服务模板字段：

```yaml
id: hysteria2
name: Hysteria2
units: ["hysteria-server", "hysteria@.*"]
binary_patterns: ["hysteria"]
version_cmd: ["version"]
transport: ["udp"]
cert_paths: ["/etc/hysteria/server.crt"]
listen_ports: [8443]
stats_kind: hysteria2
```

每台节点可同时上报多个服务。`runtime` 字段 v1 固定 `systemd`，二期 Docker 检测复用。
`listen_ok` 表示至少发现该服务归属的监听端口；模板指定 `listen_ports` 时，
再校验这些端口是否都在监听。

## 5. 上报数据模型与 sink 抽象

### 5.1 上报报文

```json
{
  "schema_version": "1",
  "agent_version": "0.1.0",
  "node_id": "a1b2c3...",
  "collected_at": "2026-08-18T12:00:00+08:00",
  "collect_ms": 180,
  "host": {
    "hostname": "node-01",
    "os": "ubuntu",
    "os_version": "22.04",
    "kernel": "5.15.0",
    "arch": "amd64",
    "uptime_seconds": 864000,
    "load1": 0.2, "load5": 0.3, "load15": 0.4,
    "mem_total_bytes": 1073741824,
    "mem_available_bytes": 536870912,
    "mem_used_pct": 50,
    "disk_used_pct": 42,
    "upgradable_count": 3
  },
  "services": [
    {
      "type": "hysteria2",
      "runtime": "systemd",
      "unit": "hysteria-server",
      "binary": "/usr/local/bin/hysteria",
      "version": "v2.9.0",
      "active": true,
      "enabled": true,
      "main_pid": 1234,
      "n_restarts": 2,
      "listen": [{"proto": "udp", "addr": "0.0.0.0", "port": 8443}],
      "listen_ok": true,
      "cert": {"not_after": "2026-09-28T00:00:00Z", "days_left": 41},
      "stats": {"tx": 0, "rx": 0},
      "status": "ok",
      "error": ""
    }
  ]
}
```

- `node_id` 由 `/etc/machine-id` 派生，取不到时生成 UUID 存配置目录，也允许手动指定；
- `schema_version` 用于 Agent 与 Panel 独立演进；
- `services` 为数组，支持一台多服务；
- `status` 取值 `ok / warn / error`；
- 未开启统计接口的服务，`stats` 省略或为 `null`。

### 5.2 sink 抽象

首版两类：

1. `panel`：POST 到 `${panel_url}/api/v1/agents/report`，带
   `Authorization: Bearer <token>` 与 `X-Agent-Id`，2xx 为成功；
2. `webhook`：POST 同一份 JSON 到任意 URL，支持自定义 Header 与可选 token。

行为：

- 每次运行向所有 sink 发送；
- 单 sink 默认 10 秒超时，运行内最多重试 2 次；
- 退出码：0 全部成功（含仅告警）；1 有 sink 失败；2 配置错误；
- v1 不做本地持久化队列，离线缓冲列入二期。

## 6. 配置文件结构

主配置 TOML，默认路径依次为 `/etc/net-probe/config.toml`、
`~/.config/net-probe/config.toml`，支持 `--config` 指定。

```toml
[agent]
node_id = ""
log_level = "info"

[[sink]]
type = "panel"
url = "https://panel.example.com"
token_env = "NET_PROBE_PANEL_TOKEN"

[[sink]]
type = "webhook"
url = "https://uptime-kuma.example/api/push/xxxx"
method = "POST"
headers = { "Authorization" = "Bearer ..." }

[collect]
disk_mounts = ["/"]
upgradable = true

[detect]
include = ["hysteria2", "xray", "v2ray", "sing-box",
           "shadowsocks", "trojan", "tuic", "anytls", "generic"]
custom_dir = "/etc/net-probe/services.d"
```

- 仅 `sink` 必填，其余有默认值；
- 敏感项支持 `*_env` 从环境变量读取或独立文件引用；
- CLI：`--version`、`--once`、`--check`（校验并打印将上报 JSON，不发送）、
  预留 `--resident`。

## 7. 安全边界与命令通道预留

v1 只读、最小权限：

- systemd 以专用用户 `net-probe` 运行，`NoNewPrivileges=true`；
- 遇 root 才可读的证书/secret 时置空并标记不可用，不中断；
- 安装脚本提供可选 root 完整检测模式并写明取舍；
- 上报不含密码、证书内容、统计 secret。

token 与传输：

- token 走 `token_env` 或 0600 文件，不落主配置明文；
- `--check` 与日志自动脱敏；
- panel sink 要求 HTTPS，非本机 `http://` 默认拒绝，除非显式放行。

命令通道（二期实现，协议 v1 预留）：

```json
{"ack": true, "commands": []}
```

命令结构预留字段：

```json
{
  "id": "cmd_123",
  "action": "restart",
  "params": {},
  "nonce": "b1a...",
  "expires_at": "2026-08-18T12:35:00Z",
  "signature": "..."
}
```

- 命令使用 Ed25519 签名：面板持私钥，Agent 配公钥验签，与上报 token 分离；
- `nonce` 一次性、`expires_at` 短时效，防重放；
- 动作白名单：`restart / upgrade / rollback / get-logs`，不接受任意 shell；
- 执行器走 sudo 白名单到固定包装脚本；
- 面板与节点双侧审计。

## 8. 错误处理与健壮性

- 单服务失败标 `status: error` 并带原因，不影响其他服务；
- 版本/证书/端口任一步失败只置空对应字段；
- 版本命令 3 秒超时，整体运行 60 秒总超时；
- sink 失败重试后仍失败只记日志并返回退出码，不崩溃；
- 日志分级并脱敏；
- v1 无持久队列，离线缓冲二期再加。

## 9. 测试与发布

测试：

- 单元测试：模板匹配、版本解析、`/proc` 端口解析、证书天数、配置加载、报告组装、sink 重试；
- 夹具测试：8 类服务的假 systemctl/proc/version 输出，覆盖实现差异；
- 集成测试：本地假 Panel 接收并校验报告 schema 与鉴权头；
- CI：`go test ./...`、`go vet`、格式化检查、交叉编译验证。

发布：

- goreleaser 产出 `linux-amd64`、`linux-arm64` 单文件二进制与 checksum；
- 一键安装脚本：检测架构 → 下载 → 写 systemd service + timer → 引导配置；
- 附 README、配置参考、检测模板编写指南、安全说明、LICENSE；
- semver 版本，报告携带 `schema_version` 与 `agent_version`。

## 10. 已确认的决策

- 项目拆分为 Agent / Panel / 分发，先做 Agent；
- Agent 形式：静态 Go 二进制 + timer，预留 resident；
- systemd 与 Docker 分期：v1 systemd/原生，`runtime` 字段预留 Docker；
- 上报目标：自研 Panel 为主 + 通用 webhook，可插拔 sink；
- 特权动作：v1 只读，协议预留命令通道，二期实现执行器；
- 自动检测代替用户预选实现，`generic` 兜底；
- 主配置 TOML，检测模板 YAML。

## 11. 待定事项

- 项目名暂定为 `net-probe`，首版发布前如无异议则沿用；
- Panel 子项目的具体设计：作为下一子项目单独出设计文档；
- 命令动作最终白名单：二期实现执行器时确定（签名算法已定为 Ed25519）。
