# Net-probe Panel 设计文档

- 日期：2026-08-18
- 状态：已确认待审阅
- 范围：Panel 子项目（中心面板），并包含少量 Agent 侧配套改动

## 1. 背景与目标

Panel 是 net-probe 的中心管理面板，接收各台 VPS 上 Agent 的上报，
提供节点/服务/历史/告警的统一视图，并支持后续对节点下发命令。

目标：

- 单二进制、轻量、易一键部署；
- 7–100 台 VPS 规模下保持低资源占用；
- 可视化节点与服务状态、历史趋势、流量；
- 提供可配置告警与 Telegram/webhook 通知；
- 为 per-node token 与命令通道预留扩展位。

## 2. 范围与非目标

范围内（v1）：

- 接收 Agent 上报、自动注册节点；
- 节点/服务/历史/告警/版本管理页面；
- 单管理员密码登录；
- 六类告警规则与 Telegram/webhook 通知；
- SQLite 分层保留与聚合；
- 单二进制 + 内嵌前端 + 自签证书部署。

非目标（v1）：

- 每节点独立 token（二期）；
- 命令下发（重启/升级，二期）；
- SSH Key / 2FA 登录（二期扩展）；
- 多用户与角色（二期）；
- 流量异常检测（二期）。

## 3. 架构与运行模型

Panel 是单二进制 `net-probe-panel`，一个进程包含：

- HTTP 服务：Agent 上报 API + 管理 API + 内嵌前端静态资源；
- SQLite：纯 Go 驱动 `modernc.org/sqlite`，无 CGO、无独立数据库进程；
- 后台调度：最新版本拉取、原始数据聚合/清理、告警评估与通知；
- 前端：预构建静态 SPA，`go:embed` 进二进制，无 Node 运行时。

数据流：

```text
Agent POST /api/v1/agents/report
  -> 共享 token 鉴权 -> 校验 JSON
  -> upsert 节点 + 写最新状态 + 写历史
  -> 告警评估 -> Telegram/webhook 通知
```

运行形态：

- `net-probe-panel` 系统用户运行；
- 数据目录 `/var/lib/net-probe-panel`（可配）；
- 首次启动自动生成长期自签证书，默认监听 `:8443`；
- `listen_addr`、证书路径、数据目录、`node_timeout`、保留时长、告警阈值、
  通知渠道均通过 TOML 配置。

## 4. 数据模型

| 表 | 作用 | 关键字段 |
|---|---|---|
| `users` | 管理员账号 | `username` 唯一、`password_hash` |
| `sessions` | 登录会话 | `token` 主键、`user_id`、`expires_at` |
| `nodes` | 节点最新状态 | `node_id` 唯一、`alias`、`token`（预留 per-node）、`muted_until`、`last_report_at`、`last_host_json`、`last_services_json` |
| `tags` / `node_tags` | 标签与分组 | `tags.name` 唯一；多对多 |
| `metrics` | 历史指标 | `node_id`、`ts`、`granularity`（raw/hourly/daily）、`load1/5/15`、`mem_used_pct`、`disk_used_pct`、`services_json` |
| `alerts` | 告警实例 | `node_id`、`rule`、`status`（firing/recovered/acknowledged）、`message`、首末时间 |
| `versions` | 各服务最新版本 | `service_type` 主键、`latest_version`、`source`（github/manual）、`updated_at` |

说明：

- `nodes.last_host_json` / `last_services_json` 用于列表与详情页快速渲染；
- `metrics.granularity` 支持原始 7 天、小时 30 天、天 1 年（时长可配）；
- 告警规则阈值放 Panel 配置 TOML，不落库；
- `versions` 由后台定时从官方源更新，可手动覆盖。

## 5. API 设计

Agent 上报（共享 token）：

- `POST /api/v1/agents/report`
  - 请求体：Agent 上报 JSON；
  - 响应体预留：`{"ack":true,"commands":[]}`。

管理 API（Session Cookie，前缀 `/api/v1/admin`）：

- `POST /login`、`POST /logout`
- `GET /overview`
- `GET /nodes`（`status/tag/q` 过滤、分页）
- `GET /nodes/{id}`
- `PATCH /nodes/{id}`（别名、标签、静音）
- `DELETE /nodes/{id}`
- `GET /nodes/{id}/metrics`（`granularity/from/to`）
- `GET /alerts`、`POST /alerts/{id}/ack`
- `GET /versions`、`PATCH /versions/{service_type}`
- `GET /settings`

命令通道（二期预留）：

- `POST /nodes/{id}/commands`。

鉴权与错误：

- 管理接口用 HttpOnly Session Cookie；
- Agent 接口用 `Authorization: Bearer <token>`；
- 统一 JSON 错误：`{"error":{"code":"...","message":"..."}}`。

## 6. 告警、版本源与统计集成

### 6.1 告警规则

| 规则 | 触发条件 |
|---|---|
| `node_offline` | `now - last_report_at > node_timeout` |
| `service_down` | 任一服务 `active=false` 或 `status=error` |
| `cert_expiry` | 证书剩余天数 `< 7` |
| `disk_usage` | `disk_used_pct > 85` |
| `mem_usage` | `mem_used_pct > 90` |
| `version_lag` | 服务版本落后于 `versions.latest_version` |

状态机：

```text
正常 -> firing（触发通知）
firing -> recovered（恢复通知）
firing/recovered -> acknowledged（管理员确认）
```

节点 `muted_until` 内不评估、不通知。

### 6.2 版本源

后台默认每 12 小时从 GitHub Releases 拉取最新版本：

- Hysteria2：`apernet/hysteria`
- Xray：`XTLS/Xray-core`
- sing-box：`SagerNet/sing-box`
- V2Ray：`v2fly/v2ray-core`

其余类型首版以手动覆盖为主，后续补自动源。手动覆盖优先级高于自动拉取。

### 6.3 统计集成（流量/在线连接）

Agent 侧按 `stats_kind` 采集并写入 `service.stats`：

- Hysteria2：Traffic Stats API（`/traffic`、`/online`）
- Xray：gRPC Stats API
- sing-box：Clash/v2ray API

Panel 的数据契约：每个服务可带 `stats: {tx, rx, online_clients}`；
Panel 负责存储、展示与趋势图，采集由 Agent 实现。

## 7. 认证与安全

- v1 单管理员密码登录（bcrypt 密码哈希），无 2FA、无 SSH Key 登录；
- 管理会话用 HttpOnly Session Cookie；
- Agent 上报用共享 token，`nodes.token` 预留 per-node token（二期）；
- Panel 首次启动生成长期自签证书，Agent 的 panel sink 增加
  `tls_skip_verify`（或 `tls_ca_file`）以信任自签证书；
- 面板监听地址、证书路径、数据目录可配。

## 8. 部署形态

- 单二进制 `net-probe-panel` + SQLite + 内嵌前端；
- systemd 服务运行，数据目录 `/var/lib/net-probe-panel`；
- 提供一键安装脚本（后续 README 更新）；
- 自签证书 HTTPS，无域名时用 IP 部署，Agent 侧开启 `tls_skip_verify`。

## 9. 前端页面与 UI/UX

风格：Data-Dense Dashboard（暗色为主，支持亮色），高信息密度，动效克制。

设计 Token：

| 角色 | 值 |
|---|---|
| 背景 | `#020617` |
| 卡片/次级 | `#1E293B` |
| 文字 | `#F8FAFC` |
| 边框 | `#334155` |
| 主色 | `#0F172A` |
| 强调/成功 | `#22C55E` |
| 警告 | `#EAB308` |
| 危险 | `#EF4444` |

字体：标题 `Fira Code`，正文 `Fira Sans`；图标用 SVG（Lucide），不用 emoji。

页面：

1. `/login` 登录页
2. `/overview` 总览：4 个可点击 KPI 卡片 + 服务类型分布条形图 + 节点列表
3. `/nodes` 节点列表：表格/卡片切换、状态徽章、标签过滤、搜索、分页
4. `/nodes/:id` 节点详情：主机信息、服务卡、资源趋势折线图、流量面积图、告警
5. `/alerts` 告警：表格 + 过滤 + 确认
6. `/versions` 版本管理：表格 + 手动覆盖
7. `/settings` 设置：分组表单

节点列表列：名称 / IP / 服务 / 版本 / 状态 / 最后上报（服务与版本分列）。

图表选型：

- 趋势：折线图/面积图（Recharts）
- 服务类型分布：条形图
- 流量：面积图
- 异常/告警点：折线加高亮标记

前端技术栈：

- React + Vite + Tailwind CSS + shadcn/ui + Lucide + Recharts；
- 构建为静态文件后 `go:embed`。

视觉稿：`docs/panel-preview.html`（布局、配色、字体、图表方向已确认）。

## 10. 已确认的决策

- 节点：共享 token（预留 per-node），`node_timeout` 默认 `3m`，别名+标签/分组，
  操作含查看/删除/编辑/静音；
- 服务：独立展示版本；增加流量与在线连接；版本对比由 Panel 集中拉取；
- 统计采集：Hysteria2 + Xray + sing-box；
- 历史：原始 7d / 小时 30d / 天 1y（可配），存主机指标+服务状态+流量；
- 告警：六类规则，Telegram/webhook，状态转换通知 + ack + 节点静音；
- 认证：单管理员密码登录，无 2FA，Session Cookie；
- 部署：单二进制 + SQLite + 内嵌前端 + systemd 一键安装；自签证书。

## 11. Agent 侧配套改动

以下改动属于 Agent，但由 Panel 设计引出：

- 增加 `service.stats`（tx/rx/online_clients）采集，覆盖 Hysteria2/Xray/sing-box；
- panel sink 增加 `tls_skip_verify`（或 `tls_ca_file`）以信任自签证书；
- 上报响应已预留 `commands`，二期实现命令通道。

## 12. 二期预留

- per-node token（单独禁用/吊销）；
- 命令下发（重启/升级/回滚）；
- SSH Key / 2FA 登录；
- 多用户与角色；
- 邮件通知；
- 流量异常检测；
- 标签/分组的更复杂组织能力。
