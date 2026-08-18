# Net-probe Agent 配套改动 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Agent 能采集 Hysteria2 / sing-box / Xray 的流量与在线连接数，并支持连接自签证书的 Panel（`tls_skip_verify` / `tls_ca_file`）。

**Architecture:** 在 `config` 增加 `[stats]` 配置与 sink TLS 选项；在 `detect` 增加按 `stats_kind` 分发的统计采集器，检测编排时按配置补齐 `service.stats`。

**Tech Stack:** Go 1.23、标准库 net/http、encoding/json。

## Global Constraints

- module path：`github.com/s2005lg/net-probe`
- `report.Stats` 新增 `OnlineClients uint64`，JSON 字段 `online_clients`，保留 `tx/rx`
- 未配置 `[stats].services.<type>` 的服务不采集，`stats` 留空
- sink TLS 选项仅影响 panel/webhook 的 `http.Client`
- 每个任务结束前运行 `gofmt -w .` 和 `go test ./...`

---

## File Structure

```text
internal/report/types.go          (modify)
internal/config/config.go         (modify)
internal/config/config_test.go    (modify)
internal/sink/sink.go             (modify)
internal/sink/sink_test.go        (modify)
internal/detect/stats.go          (create)
internal/detect/stats_test.go     (create)
internal/detect/detect.go         (modify)
```

---

## Task 1: 扩展 `report.Stats` 增加在线连接数

**Files:**
- Modify: `internal/report/types.go`
- Modify: `internal/report/types_test.go`

**Interfaces:**
- Produces: `report.Stats{ Tx, Rx uint64; OnlineClients uint64 }`

- [ ] **Step 1: 写失败测试**

```go
func TestStatsOnlineClients(t *testing.T) {
	b, err := json.Marshal(Stats{Tx: 10, Rx: 20, OnlineClients: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"online_clients":3`) {
		t.Fatalf("json = %s", b)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/report`
Expected: FAIL（`OnlineClients` 未定义）

- [ ] **Step 3: 实现字段**

`internal/report/types.go` 的 `Stats` 增加：

```go
type Stats struct {
	Tx            uint64 `json:"tx"`
	Rx            uint64 `json:"rx"`
	OnlineClients uint64 `json:"online_clients,omitempty"`
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/report`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/report/types.go internal/report/types_test.go
git commit -m "feat: add online_clients to report stats"
```

---

## Task 2: 配置增加 `[stats]` 段

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.StatsConfig`、`config.StatsService`、`(*Config).Stats`

- [ ] **Step 1: 写失败测试**

```go
func TestStatsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[[sink]]
type = "webhook"
url = "https://example.com/report"

[stats.services.hysteria2]
endpoint = "http://127.0.0.1:9999"
secret = "s3cret"
`
	_ = os.WriteFile(path, []byte(content), 0o600)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := cfg.Stats.Services["hysteria2"]
	if !ok || s.Endpoint != "http://127.0.0.1:9999" || s.Secret != "s3cret" {
		t.Fatalf("stats = %+v", cfg.Stats.Services)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/config`
Expected: FAIL（`Stats` 字段未定义）

- [ ] **Step 3: 实现配置**

`internal/config/config.go`：

```go
type StatsService struct {
	Endpoint string `toml:"endpoint"`
	Secret   string `toml:"secret"`
}

type StatsConfig struct {
	Services map[string]StatsService `toml:"services"`
}

type Config struct {
	Agent   AgentConfig   `toml:"agent"`
	Sinks   []Sink        `toml:"sink"`
	Collect CollectConfig `toml:"collect"`
	Detect  DetectConfig  `toml:"detect"`
	Stats   StatsConfig   `toml:"stats"`
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/config`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add stats config section"
```

---

## Task 3: sink 增加 TLS 信任选项

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/sink/sink.go`
- Modify: `internal/sink/sink_test.go`

**Interfaces:**
- Produces: `config.Sink.TLSSkipVerify`、`config.Sink.TLSCAFile`

- [ ] **Step 1: 写失败测试**

```go
func TestSinkTLSConfig(t *testing.T) {
	t.Setenv("T", "s")
	s, err := New(config.Sink{Type: "webhook", URL: "https://example.com", TokenEnv: "T", TLSSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	hs := s.(*httpSink)
	tr, ok := hs.client.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLSSkipVerify not applied")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/sink`
Expected: FAIL（`TLSSkipVerify` 未定义）

- [ ] **Step 3: 实现 TLS 配置**

`internal/config/config.go` 的 `Sink` 增加：

```go
TLSSkipVerify bool   `toml:"tls_skip_verify"`
TLSCAFile     string `toml:"tls_ca_file"`
```

`internal/sink/sink.go` 的 `New` 里构建带 TLS 配置的 `http.Transport`：

```go
tlsCfg := &tls.Config{InsecureSkipVerify: cfg.TLSSkipVerify} // #nosec G402 — 用户显式开启
if cfg.TLSCAFile != "" {
	b, err := os.ReadFile(cfg.TLSCAFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("bad CA file %s", cfg.TLSCAFile)
	}
	tlsCfg.RootCAs = pool
}
tr := &http.Transport{TLSClientConfig: tlsCfg}
client := &http.Client{Timeout: 10 * time.Second, Transport: tr}
```

（`import` 增加 `crypto/tls`、`crypto/x509`。）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/sink`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/config.go internal/sink/sink.go internal/sink/sink_test.go
git commit -m "feat: add tls trust options to sinks"
```

---

## Task 4: Hysteria2 统计采集器

**Files:**
- Create: `internal/detect/stats.go`
- Create: `internal/detect/stats_test.go`

**Interfaces:**
- Produces: `detect.CollectStats(ctx, kind string, endpoint, secret string) (*report.Stats, error)`

- [ ] **Step 1: 写失败测试**

```go
func TestHysteria2Stats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/traffic":
			_, _ = w.Write([]byte(`{"u1":{"tx":100,"rx":50},"u2":{"tx":300,"rx":150}}`))
		case "/online":
			_, _ = w.Write([]byte(`{"u1":2,"u2":1}`))
		}
	}))
	defer srv.Close()
	s, err := CollectStats(context.Background(), "hysteria2", srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Tx != 400 || s.Rx != 200 || s.OnlineClients != 3 {
		t.Fatalf("stats = %+v", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/detect -run TestHysteria2Stats`
Expected: FAIL（`CollectStats` 未定义）

- [ ] **Step 3: 实现 Hysteria2 统计**

`internal/detect/stats.go`：

```go
package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/s2005lg/net-probe/internal/report"
)

func CollectStats(ctx context.Context, kind, endpoint, secret string) (*report.Stats, error) {
	switch kind {
	case "hysteria2":
		return hysteria2Stats(ctx, endpoint, secret)
	default:
		return nil, fmt.Errorf("unsupported stats kind %q", kind)
	}
}

func hysteria2Stats(ctx context.Context, endpoint, secret string) (*report.Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	txRx := map[string]struct {
		Tx uint64 `json:"tx"`
		Rx uint64 `json:"rx"`
	}{}
	if err := getJSON(ctx, client, endpoint+"/traffic", secret, &txRx); err != nil {
		return nil, err
	}
	online := map[string]uint64{}
	if err := getJSON(ctx, client, endpoint+"/online", secret, &online); err != nil {
		return nil, err
	}
	s := &report.Stats{}
	for _, v := range txRx {
		s.Tx += v.Tx
		s.Rx += v.Rx
	}
	for _, n := range online {
		s.OnlineClients += n
	}
	return s, nil
}

func getJSON(ctx context.Context, client *http.Client, url, secret string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("stats %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/detect -run TestHysteria2Stats`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/detect/stats.go internal/detect/stats_test.go
git commit -m "feat: add hysteria2 stats collector"
```

---

## Task 5: sing-box 统计采集器

**Files:**
- Modify: `internal/detect/stats.go`
- Modify: `internal/detect/stats_test.go`

**Interfaces:**
- Consumes: Task 4 的 `CollectStats`、`getJSON`

- [ ] **Step 1: 写失败测试**

```go
func TestSingBoxStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"up":700,"down":300}`))
	}))
	defer srv.Close()
	s, err := CollectStats(context.Background(), "sing-box", srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Tx != 700 || s.Rx != 300 {
		t.Fatalf("stats = %+v", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/detect -run TestSingBoxStats`
Expected: FAIL（`sing-box` 分支未实现）

- [ ] **Step 3: 实现 sing-box 统计**

`CollectStats` 的 `switch` 增加：

```go
case "sing-box":
	return singBoxStats(ctx, endpoint, secret)
```

并新增：

```go
func singBoxStats(ctx context.Context, endpoint, secret string) (*report.Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	v := struct {
		Up   uint64 `json:"up"`
		Down uint64 `json:"down"`
	}{}
	if err := getJSON(ctx, client, endpoint+"/traffic", secret, &v); err != nil {
		return nil, err
	}
	return &report.Stats{Tx: v.Up, Rx: v.Down}, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/detect -run TestSingBoxStats`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/detect/stats.go internal/detect/stats_test.go
git commit -m "feat: add sing-box stats collector"
```

---

## Task 6: Xray 统计采集器（CLI 方式）

**Files:**
- Modify: `internal/detect/stats.go`
- Modify: `internal/detect/stats_test.go`

**Interfaces:**
- Consumes: `Runner`（Task 4/5 已有）

- [ ] **Step 1: 写失败测试**

```go
func TestXrayStats(t *testing.T) {
	r := fakeRunner{out: map[string]string{
		"xray api stats query -s 127.0.0.1:8080": "user>>>u1>>>traffic>>>uplink 700\nuser>>>u1>>>traffic>>>downlink 300\n",
	}}
	s, err := xrayStats(context.Background(), r, "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	if s.Tx != 700 || s.Rx != 300 {
		t.Fatalf("stats = %+v", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/detect -run TestXrayStats`
Expected: FAIL（`xrayStats` 未定义）

- [ ] **Step 3: 实现 Xray 统计**

`CollectStats` 增加签名（Xray 需要 Runner）：

```go
func CollectStatsWithRunner(ctx context.Context, kind string, endpoint, secret string, r Runner) (*report.Stats, error) {
	switch kind {
	case "hysteria2":
		return hysteria2Stats(ctx, endpoint, secret)
	case "sing-box":
		return singBoxStats(ctx, endpoint, secret)
	case "xray":
		return xrayStats(ctx, r, endpoint)
	default:
		return nil, fmt.Errorf("unsupported stats kind %q", kind)
	}
}
```

`CollectStats` 保留为无 Runner 版本，Xray 返回错误提示需用 `CollectStatsWithRunner`。

新增：

```go
func xrayStats(ctx context.Context, r Runner, server string) (*report.Stats, error) {
	out, err := r.Run(ctx, "xray", "api", "stats", "query", "-s", server)
	if err != nil {
		return nil, err
	}
	s := &report.Stats{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[len(f)-1], 10, 64)
		if strings.Contains(line, "uplink") {
			s.Tx += v
		}
		if strings.Contains(line, "downlink") {
			s.Rx += v
		}
	}
	return s, nil
}
```

（`import` 增加 `strconv`。）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/detect -run TestXrayStats`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/detect/stats.go internal/detect/stats_test.go
git commit -m "feat: add xray stats collector via cli"
```

---

## Task 7: 统计端点自动发现与编排接入

**Files:**
- Modify: `internal/detect/template.go`（新增 `StatsConfigPaths`）
- Modify: `internal/detect/builtin/hysteria2.yaml`、`sing-box.yaml`、`xray.yaml`（新增 `stats_config_paths`）
- Modify: `internal/detect/stats.go`（新增 `discoverStats`）
- Modify: `internal/detect/detect.go`（接入统计，优先用户覆盖，其次自动发现）
- Modify: `internal/detect/detect_test.go`、`internal/detect/stats_test.go`

**Interfaces:**
- Consumes: `config.StatsConfig`、`CollectStatsWithRunner`、`Template.StatsConfigPaths`
- Produces: `detect.discoverStats(tmpl Template) (StatsEndpoint, bool)`、`detect.statsEndpointFor(tmpl, statsCfg) (endpoint, secret string, ok bool)`

- [ ] **Step 1: 写失败测试**

```go
func TestDiscoverStats(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "hysteria.yaml")
	_ = os.WriteFile(cfg, []byte("trafficStats:\n  listen: 127.0.0.1:9999\n  secret: abc\n"), 0o600)
	tmpl := Template{StatsKind: "hysteria2", StatsConfigPaths: []string{cfg}}
	ep, ok := discoverStats(tmpl)
	if !ok || ep.Endpoint != "127.0.0.1:9999" || ep.Secret != "abc" {
		t.Fatalf("ep=%+v ok=%v", ep, ok)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/detect -run TestDiscoverStats`
Expected: FAIL（`discoverStats` 未定义）

- [ ] **Step 3: 实现自动发现与接入**

`internal/detect/template.go` 的 `Template` 增加：

```go
StatsConfigPaths []string `yaml:"stats_config_paths"`
```

`internal/detect/builtin/hysteria2.yaml` 增加：

```yaml
stats_config_paths: ["/etc/hysteria/config.yaml", "/etc/hysteria/config.yml"]
```

`sing-box.yaml` 增加：

```yaml
stats_config_paths: ["/etc/sing-box/config.json", "/etc/sing-box/config.yaml"]
```

`xray.yaml` 增加：

```yaml
stats_config_paths: ["/usr/local/etc/xray/config.json", "/etc/xray/config.json"]
```

`internal/detect/stats.go` 新增：

```go
type StatsEndpoint struct {
	Endpoint string
	Secret   string
}

func discoverStats(tmpl Template) (StatsEndpoint, bool) {
	for _, p := range tmpl.StatsConfigPaths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal(b, &m); err != nil {
			continue
		}
		switch tmpl.StatsKind {
		case "hysteria2":
			ts, _ := m["trafficStats"].(map[string]any)
			if ts == nil {
				continue
			}
			listen, _ := ts["listen"].(string)
			secret, _ := ts["secret"].(string)
			if listen != "" {
				return StatsEndpoint{Endpoint: listen, Secret: secret}, true
			}
		case "sing-box":
			exp, _ := m["experimental"].(map[string]any)
			if exp == nil {
				continue
			}
			clash, _ := exp["clash_api"].(map[string]any)
			if clash == nil {
				continue
			}
			ctrl, _ := clash["external_controller"].(string)
			secret, _ := clash["secret"].(string)
			if ctrl != "" {
				return StatsEndpoint{Endpoint: ctrl, Secret: secret}, true
			}
		}
	}
	return StatsEndpoint{}, false
}
```

（`stats.go` import 增加 `os`、`gopkg.in/yaml.v3`。）

`internal/detect/detect.go` 的 `Detect` 增加 `statsCfg config.StatsConfig` 参数，并在服务构建处接入：

```go
if tmpl.StatsKind != "" {
	if endpoint, secret, ok := statsEndpointFor(tmpl, statsCfg); ok {
		if st, err := CollectStatsWithRunner(ctx, tmpl.StatsKind, endpoint, secret, deps.Runner); err == nil {
			svc.Stats = st
		}
	}
}

func statsEndpointFor(tmpl Template, statsCfg config.StatsConfig) (string, string, bool) {
	if sc, ok := statsCfg.Services[tmpl.StatsKind]; ok && sc.Endpoint != "" {
		return sc.Endpoint, sc.Secret, true
	}
	if ep, ok := discoverStats(tmpl); ok {
		return ep.Endpoint, ep.Secret, true
	}
	return "", "", false
}
```

同步更新调用点：`internal/agent/run.go` 的 `Build` 传入 `cfg.Stats`，其余测试传入空 `config.StatsConfig{}`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/detect/template.go internal/detect/builtin/*.yaml internal/detect/stats.go internal/detect/stats_test.go internal/detect/detect.go internal/detect/detect_test.go internal/agent/run.go
git commit -m "feat: auto-discover stats endpoints and integrate stats collection"
```

---

## Self-Review 记录

- 覆盖：`report.Stats.online_clients`、`[stats]` 用户覆盖、sink TLS 信任、统计端点自动发现（Hysteria2/sing-box）、Hysteria2/sing-box/Xray 采集、编排接入均映射到任务。
- 类型一致性：`CollectStats` / `CollectStatsWithRunner` 在 Task 4/6 定义，Task 7 使用；`config.StatsConfig` 在 Task 2 定义；`Template.StatsConfigPaths` 在 Task 7 定义并写入内置 YAML。
- 注意：Xray 的 CLI 输出格式以实际 `xray api stats query` 为准；Xray 端点首版主要靠 `[stats]` 覆盖，自动解析以后补充。
