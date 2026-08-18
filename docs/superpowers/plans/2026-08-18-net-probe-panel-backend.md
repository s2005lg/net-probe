# Net-probe Panel 后端 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 net-probe-panel 后端：接收 Agent 上报，提供管理 API，SQLite 存储、告警、版本源、聚合与自签 TLS，打包为单二进制。

**Architecture:** 单 Go 进程，`net/http` 提供 agent API + admin API + 内嵌前端；`modernc.org/sqlite` 无 CGO 存储；后台 goroutine 执行版本拉取、历史聚合、告警评估。

**Tech Stack:** Go 1.23、modernc.org/sqlite、golang.org/x/crypto/bcrypt、BurntSushi/toml、标准库 net/http。

## Global Constraints

- module path：`github.com/s2005lg/net-probe`
- Panel 二进制入口：`cmd/net-probe-panel/main.go`
- SQLite 文件位于 `data_dir/net-probe-panel.db`
- Agent 上报鉴权：`Authorization: Bearer <agent_token>`
- 管理接口鉴权：HttpOnly Cookie `panel_session`
- 报告 JSON 遵循 `schema_version: "1"`
- 每个任务结束前运行 `gofmt -w .` 和 `go test ./...`

---

## File Structure

```text
cmd/net-probe-panel/main.go
internal/panel/config/config.go
internal/panel/config/config_test.go
internal/panel/db/db.go
internal/panel/db/db_test.go
internal/panel/auth/auth.go
internal/panel/auth/auth_test.go
internal/panel/api/api.go
internal/panel/api/report.go
internal/panel/api/admin.go
internal/panel/api/report_test.go
internal/panel/api/admin_test.go
internal/panel/alert/alert.go
internal/panel/alert/alert_test.go
internal/panel/version/version.go
internal/panel/version/version_test.go
internal/panel/retention/retention.go
internal/panel/retention/retention_test.go
systemd/net-probe-panel.service
install-panel.sh
```

---

## Task 1: 依赖、配置与 SQLite 迁移

**Files:**
- Modify: `go.mod`（`go get modernc.org/sqlite golang.org/x/crypto/bcrypt`）
- Create: `internal/panel/config/config.go`
- Create: `internal/panel/config/config_test.go`
- Create: `internal/panel/db/db.go`
- Create: `internal/panel/db/db_test.go`

**Interfaces:**
- Produces: `config.Load(path string) (*Config, error)`、`config.Default() *Config`、`db.Open(path string) (*sql.DB, error)`、`db.Migrate(*sql.DB) error`

- [ ] **Step 1: 写失败测试**

```go
func TestConfigLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	_ = os.WriteFile(path, []byte("[agent]\ntoken = \"t\"\n[admin]\nuser = \"admin\"\n"), 0o600)
	cfg, err := Load(path)
	if err != nil || cfg.Agent.Token != "t" || cfg.Admin.User != "admin" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/config`
Expected: FAIL

- [ ] **Step 3: 实现配置与迁移**

`internal/panel/config/config.go`:

```go
type Config struct {
	ListenAddr string `toml:"listen_addr"`
	DataDir    string `toml:"data_dir"`
	Agent      struct {
		Token string `toml:"token"`
	} `toml:"agent"`
	Admin struct {
		User string `toml:"user"`
	} `toml:"admin"`
	NodeTimeout string `toml:"node_timeout"`
	Retention   struct {
		RawDays   int `toml:"raw_days"`
		HourlyDays int `toml:"hourly_days"`
		DailyDays  int `toml:"daily_days"`
	} `toml:"retention"`
}

func Default() *Config {
	c := &Config{ListenAddr: ":8443", DataDir: "/var/lib/net-probe-panel", NodeTimeout: "3m"}
	c.Admin.User = "admin"
	c.Retention.RawDays, c.Retention.HourlyDays, c.Retention.DailyDays = 7, 30, 365
	return c
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
```

`internal/panel/db/db.go` 打开文件并建表：

```go
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
}

func Migrate(d *sql.DB) error {
	_, err := d.Exec(schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY, username TEXT UNIQUE, password_hash TEXT, created_at INTEGER);
CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY, user_id INTEGER, created_at INTEGER, expires_at INTEGER);
CREATE TABLE IF NOT EXISTS nodes(id INTEGER PRIMARY KEY, node_id TEXT UNIQUE, alias TEXT, token TEXT, muted_until INTEGER, last_report_at INTEGER, last_host_json TEXT, last_services_json TEXT, created_at INTEGER, updated_at INTEGER);
CREATE TABLE IF NOT EXISTS tags(id INTEGER PRIMARY KEY, name TEXT UNIQUE);
CREATE TABLE IF NOT EXISTS node_tags(node_id INTEGER, tag_id INTEGER, PRIMARY KEY(node_id, tag_id));
CREATE TABLE IF NOT EXISTS metrics(id INTEGER PRIMARY KEY AUTOINCREMENT, node_id TEXT, ts INTEGER, granularity TEXT, load1 REAL, load5 REAL, load15 REAL, mem_used_pct REAL, disk_used_pct REAL, services_json TEXT);
CREATE INDEX IF NOT EXISTS idx_metrics_node_ts ON metrics(node_id, ts);
CREATE TABLE IF NOT EXISTS alerts(id INTEGER PRIMARY KEY, node_id TEXT, rule TEXT, status TEXT, message TEXT, first_seen_at INTEGER, last_seen_at INTEGER, recovered_at INTEGER, acknowledged_at INTEGER);
CREATE TABLE IF NOT EXISTS versions(service_type TEXT PRIMARY KEY, latest_version TEXT, source TEXT, updated_at INTEGER);
`
```

（`import` 含 `modernc.org/sqlite` 匿名导入。）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/config ./internal/panel/db`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add go.mod go.sum internal/panel/config/ internal/panel/db/
git commit -m "feat: panel config and sqlite schema"
```

---

## Task 2: 认证与会话

**Files:**
- Create: `internal/panel/auth/auth.go`
- Create: `internal/panel/auth/auth_test.go`

**Interfaces:**
- Produces: `auth.HashPassword(string) (string, error)`、`auth.CheckPassword(string,string) bool`、`auth.NewSession(d, userID, ttl) (token string, error)`

- [ ] **Step 1: 写失败测试**

```go
func TestPassword(t *testing.T) {
	h, _ := HashPassword("secret")
	if !CheckPassword(h, "secret") || CheckPassword(h, "wrong") {
		t.Fatal("password check failed")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/auth`
Expected: FAIL

- [ ] **Step 3: 实现认证**

```go
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
func NewSession(d *sql.DB, userID int64, ttl time.Duration) (string, error) {
	token := randomToken()
	_, err := d.Exec(`INSERT INTO sessions(token,user_id,created_at,expires_at) VALUES(?,?,?,?)`,
		token, userID, time.Now().Unix(), time.Now().Add(ttl).Unix())
	return token, err
}
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/auth`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/panel/auth/
git commit -m "feat: panel auth and sessions"
```

---

## Task 3: Agent 上报接口

**Files:**
- Create: `internal/panel/api/report.go`
- Create: `internal/panel/api/report_test.go`

**Interfaces:**
- Consumes: `report.Report`、`db`
- Produces: `api.New(d *sql.DB, cfg *config.Config) *Server`、`(*Server).handleReport`

- [ ] **Step 1: 写失败测试**

```go
func TestHandleReport(t *testing.T) {
	d := openTestDB(t)
	s := New(d, &config.Config{})
	body := `{"schema_version":"1","agent_version":"v0.1.1","node_id":"n1","collected_at":"2026-08-18T00:00:00Z","host":{"hostname":"h"},"services":[]}`
	req := httptest.NewRequest("POST", "/api/v1/agents/report", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rr := httptest.NewRecorder()
	s.handleReport(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"ack":true`) {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/api`
Expected: FAIL

- [ ] **Step 3: 实现上报处理**

```go
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+s.cfg.Agent.Token {
		http.Error(w, `{"error":{"code":"unauthorized"}}`, 401)
		return
	}
	var rep report.Report
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil || rep.NodeID == "" {
		http.Error(w, `{"error":{"code":"bad_request"}}`, 400)
		return
	}
	hostB, _ := json.Marshal(rep.Host)
	svcB, _ := json.Marshal(rep.Services)
	now := time.Now().Unix()
	_, _ = s.db.Exec(`INSERT INTO nodes(node_id,last_report_at,last_host_json,last_services_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET last_report_at=?,last_host_json=?,last_services_json=?,updated_at=?`,
		rep.NodeID, now, string(hostB), string(svcB), now, now, now, string(hostB), string(svcB), now)
	_, _ = s.db.Exec(`INSERT INTO metrics(node_id,ts,granularity,load1,load5,load15,mem_used_pct,disk_used_pct,services_json)
		VALUES(?,?,'raw',?,?,?,?,?,?)`,
		rep.NodeID, now, rep.Host.Load1, rep.Host.Load5, rep.Host.Load15, rep.Host.MemUsedPct, rep.Host.DiskUsedPct, string(svcB))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ack":true,"commands":[]}`))
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/api`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/panel/api/
git commit -m "feat: panel agent report endpoint"
```

---

## Task 4: 管理 API（节点）

**Files:**
- Modify: `internal/panel/api/admin.go`
- Modify: `internal/panel/api/admin_test.go`

**Interfaces:**
- Produces: `(*Server).handleNodes`、`handleNodeDetail`、`handleNodePatch`、`handleNodeDelete`

- [ ] **Step 1: 写失败测试**

```go
func TestNodesList(t *testing.T) {
	// 预置节点后调用 GET /api/v1/admin/nodes，断言返回数组与 200
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/api -run TestNodesList`
Expected: FAIL

- [ ] **Step 3: 实现节点接口**

```go
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT node_id,alias,muted_until,last_report_at,last_host_json,last_services_json FROM nodes ORDER BY last_report_at DESC`)
	if err != nil {
		http.Error(w, `{"error":{"code":"db_error"}}`, 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var nodeID, alias, hostJSON, svcJSON string
		var mutedUntil, lastAt int64
		_ = rows.Scan(&nodeID, &alias, &mutedUntil, &lastAt, &hostJSON, &svcJSON)
		out = append(out, map[string]any{"node_id": nodeID, "alias": alias, "last_report_at": lastAt, "host": json.RawMessage(hostJSON), "services": json.RawMessage(svcJSON)})
	}
	writeJSON(w, 200, out)
}
```

其余 detail/patch/delete 按设计实现：detail 返回节点+最新服务；patch 更新 alias/muted；delete 删除节点及其 metrics/alerts/node_tags。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/api`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/panel/api/admin.go internal/panel/api/admin_test.go
git commit -m "feat: panel nodes admin api"
```

---

## Task 5: 历史指标与保留聚合

**Files:**
- Create: `internal/panel/retention/retention.go`
- Create: `internal/panel/retention/retention_test.go`
- Modify: `internal/panel/api/admin.go`（`/nodes/{id}/metrics`）

**Interfaces:**
- Produces: `retention.Aggregate(ctx, d, rawDays, hourlyDays, dailyDays) error`

- [ ] **Step 1: 写失败测试**

```go
func TestAggregate(t *testing.T) {
	// 插入两条 raw 记录，跑 Aggregate 后断言生成 hourly 聚合记录
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/retention`
Expected: FAIL

- [ ] **Step 3: 实现聚合**

```go
func Aggregate(ctx context.Context, d *sql.DB, rawDays, hourlyDays, dailyDays int) error {
	cutRaw := time.Now().AddDate(0, 0, -rawDays).Unix()
	_, err := d.Exec(`INSERT INTO metrics(node_id,ts,granularity,load1,load5,load15,mem_used_pct,disk_used_pct,services_json)
		SELECT node_id, (ts/3600)*3600, 'hourly', avg(load1), avg(load5), avg(load15), avg(mem_used_pct), avg(disk_used_pct), ''
		FROM metrics WHERE granularity='raw' AND ts < ? GROUP BY node_id, (ts/3600)*3600`, cutRaw)
	if err != nil {
		return err
	}
	_, err = d.Exec(`DELETE FROM metrics WHERE granularity='raw' AND ts < ?`, cutRaw)
	return err
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/retention`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/panel/retention/ internal/panel/api/admin.go
git commit -m "feat: panel metrics retention and aggregation"
```

---

## Task 6: 告警评估与通知

**Files:**
- Create: `internal/panel/alert/alert.go`
- Create: `internal/panel/alert/alert_test.go`

**Interfaces:**
- Produces: `alert.Evaluate(ctx, d, cfg, now) error`、`alert.NotifyTelegram(token, chatID, text) error`、`alert.NotifyWebhook(url, text) error`

- [ ] **Step 1: 写失败测试**

```go
func TestNodeOffline(t *testing.T) {
	// 插入 last_report_at 远早于 node_timeout 的节点，跑 Evaluate 后断言 alerts 表出现 firing
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/alert`
Expected: FAIL

- [ ] **Step 3: 实现告警**

```go
func Evaluate(ctx context.Context, d *sql.DB, cfg *config.Config, now time.Time) error {
	timeout, _ := time.ParseDuration(cfg.NodeTimeout)
	cutoff := now.Add(-timeout).Unix()
	rows, _ := d.Query(`SELECT node_id FROM nodes WHERE last_report_at < ? AND (muted_until=0 OR muted_until<?)`, cutoff, now.Unix())
	defer rows.Close()
	for rows.Next() {
		var nodeID string
		_ = rows.Scan(&nodeID)
		_, _ = d.Exec(`INSERT INTO alerts(node_id,rule,status,message,first_seen_at,last_seen_at)
			VALUES(?,'node_offline','firing','节点离线',?,?)
			ON CONFLICT DO NOTHING`, nodeID, now.Unix(), now.Unix())
	}
	return nil
}
```

通知函数用 `net/http` POST Telegram `sendMessage` 与 webhook JSON。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/alert`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/panel/alert/
git commit -m "feat: panel alert evaluation and notifications"
```

---

## Task 7: 版本源与管理接口

**Files:**
- Create: `internal/panel/version/version.go`
- Create: `internal/panel/version/version_test.go`
- Modify: `internal/panel/api/admin.go`

**Interfaces:**
- Produces: `version.FetchLatest(serviceType string) (string, error)`（Hysteria2/Xray/sing-box/V2Ray 的 GitHub Releases）

- [ ] **Step 1: 写失败测试**

```go
func TestFetchHysteria2(t *testing.T) {
	// 用 httptest 模拟 GitHub API，断言解析出 app/v2.12.1 -> 2.12.1
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/version`
Expected: FAIL

- [ ] **Step 3: 实现版本拉取**

```go
func FetchLatest(serviceType string) (string, error) {
	repos := map[string]string{
		"hysteria2": "apernet/hysteria",
		"xray":      "XTLS/Xray-core",
		"sing-box":  "SagerNet/sing-box",
		"v2ray":     "v2fly/v2ray-core",
	}
	repo, ok := repos[serviceType]
	if !ok {
		return "", fmt.Errorf("unsupported service %q", serviceType)
	}
	resp, err := http.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimPrefix(v.TagName, "app/"), "v"), nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/version`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/panel/version/ internal/panel/api/admin.go
git commit -m "feat: panel version source and api"
```

---

## Task 8: TLS 自签、路由与主入口

**Files:**
- Create: `cmd/net-probe-panel/main.go`
- Create: `internal/panel/api/api.go`

**Interfaces:**
- Produces: `api.New(d, cfg) *Server`、`(*Server).Routes() http.Handler`

- [ ] **Step 1: 写失败测试**

```go
func TestRoutes(t *testing.T) {
	// 断言 /api/v1/agents/report 与 /api/v1/admin/* 路由可达
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/panel/api`
Expected: FAIL

- [ ] **Step 3: 实现路由与主入口**

`main.go` 启动流程：加载配置 → 打开/迁移 DB → 若 users 空则用 env `NET_PROBE_PANEL_ADMIN_PASSWORD` 创建 admin → 启动后台调度（版本、聚合、告警）→ 若证书不存在则 `generateSelfSigned()` → `ListenAndServeTLS`。

```go
func generateSelfSigned(certPath, keyPath string) error {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"net-probe-panel"}}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil { return err }
	// 写 cert.pem 与 key.pem（0600）
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add cmd/net-probe-panel/ internal/panel/api/api.go
git commit -m "feat: panel server entrypoint and self-signed tls"
```

---

## Task 9: systemd 与一键安装

**Files:**
- Create: `systemd/net-probe-panel.service`
- Create: `install-panel.sh`

- [ ] **Step 1: 创建 systemd 单元**

```ini
[Unit]
Description=net-probe-panel
After=network-online.target

[Service]
Type=simple
User=net-probe-panel
ExecStart=/usr/local/bin/net-probe-panel --config /etc/net-probe-panel/config.toml
Restart=on-failure
NoNewPrivileges=true
```

- [ ] **Step 2: 创建 install-panel.sh**

与 agent 安装脚本一致：下载最新 `net-probe-panel_linux_${arch}`，创建 `net-probe-panel` 用户与数据目录，写入默认配置，安装并启用 systemd 服务。

- [ ] **Step 3: 语法与构建验证**

Run: `bash -n install-panel.sh && go test ./... && go build ./...`
Expected: 全部通过

- [ ] **Step 4: 提交**

```bash
git add systemd/net-probe-panel.service install-panel.sh
git commit -m "feat: panel systemd unit and installer"
```

---

## Self-Review 记录

- 覆盖：配置/DB、认证、上报、节点 API、历史/聚合、告警/通知、版本源、TLS/路由、systemd/安装脚本均映射到任务。
- 类型一致性：`config.Config` 在 Task 1 定义，后续任务复用；`Server` 在 Task 3 定义，Task 4/5/7/8 复用。
