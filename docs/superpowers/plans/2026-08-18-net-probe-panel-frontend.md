# Net-probe Panel 前端 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 Panel 的 React SPA：登录、总览、节点列表、节点详情、告警、版本管理、设置，构建后内嵌进 `net-probe-panel` 二进制。

**Architecture:** Vite + React + Tailwind CSS + shadcn/ui + Lucide + Recharts；构建产物 `web/dist` 通过 `go:embed` 嵌入，后端对非 API 路径回退到 `index.html`。

**Tech Stack:** Node 20+、React 18、Vite、Tailwind CSS、shadcn/ui、react-router-dom、lucide-react、recharts。

## Global Constraints

- 前端目录：`web/`
- 设计 Token 与 `docs/panel-preview.html` 一致：暗色 `#020617` 背景、`#1E293B` 卡片、`#22C55E` 成功、`#EAB308` 警告、`#EF4444` 危险
- 字体：标题 `Fira Code`，正文 `Fira Sans`；图标用 Lucide SVG，不用 emoji
- 管理 API 基路径：`/api/v1/admin`；请求带 `credentials: "include"`
- 所有可点击元素有 hover/focus 状态，动效 150–300ms，尊重 `prefers-reduced-motion`
- 每个任务结束前运行 `npm run build`（或 `pnpm build`）

---

## File Structure

```text
web/
  package.json
  vite.config.ts
  tailwind.config.ts
  index.html
  src/
    main.tsx
    App.tsx
    lib/api.ts
    styles.css
    components/Layout.tsx
    pages/Login.tsx
    pages/Overview.tsx
    pages/Nodes.tsx
    pages/NodeDetail.tsx
    pages/Alerts.tsx
    pages/Versions.tsx
    pages/Settings.tsx
```

---

## Task 1: 脚手架与设计 Token

**Files:**
- Create: `web/package.json`、`vite.config.ts`、`tailwind.config.ts`、`index.html`、`src/main.tsx`、`src/styles.css`

- [ ] **Step 1: 初始化依赖**

```bash
cd web && npm create vite@latest . -- --template react-ts
npm install
npm install tailwindcss @tailwindcss/vite react-router-dom lucide-react recharts
```

- [ ] **Step 2: 配置 Tailwind 与 token**

`src/styles.css`:

```css
@import "tailwindcss";
:root {
  --bg: #020617; --card: #1E293B; --border: #334155;
  --text: #F8FAFC; --muted: #94A3B8;
  --accent: #22C55E; --warn: #EAB308; --danger: #EF4444;
  --font-head: "Fira Code", monospace; --font-body: "Fira Sans", sans-serif;
}
body { background: var(--bg); color: var(--text); font-family: var(--font-body); }
```

- [ ] **Step 3: 构建确认**

Run: `npm run build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add web/package.json web/package-lock.json web/vite.config.ts web/tailwind.config.ts web/index.html web/src/
git commit -m "feat: scaffold panel frontend"
```

---

## Task 2: 应用外壳、路由与 API 客户端

**Files:**
- Create: `src/App.tsx`、`src/components/Layout.tsx`、`src/lib/api.ts`

- [ ] **Step 1: 写失败构建检查**

先创建 `src/lib/api.ts`：

```ts
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1/admin${path}`, { credentials: "include", headers: { "Content-Type": "application/json" }, ...init });
  if (!res.ok) throw new Error((await res.json())?.error?.message ?? res.statusText);
  return res.json();
}
```

- [ ] **Step 2: 创建布局与路由**

`src/App.tsx`:

```tsx
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Layout from "./components/Layout";
import Login from "./pages/Login";
import Overview from "./pages/Overview";
import Nodes from "./pages/Nodes";
import NodeDetail from "./pages/NodeDetail";
import Alerts from "./pages/Alerts";
import Versions from "./pages/Versions";
import Settings from "./pages/Settings";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route element={<Layout />}>
          <Route path="/overview" element={<Overview />} />
          <Route path="/nodes" element={<Nodes />} />
          <Route path="/nodes/:id" element={<NodeDetail />} />
          <Route path="/alerts" element={<Alerts />} />
          <Route path="/versions" element={<Versions />} />
          <Route path="/settings" element={<Settings />} />
        </Route>
        <Route path="*" element={<Navigate to="/overview" replace />} />
      </Routes>
    </BrowserRouter>
  );
}
```

`Layout.tsx` 侧边栏按 7 个页面渲染导航，`<Outlet/>` 展示内容。

- [ ] **Step 3: 构建确认**

Run: `npm run build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add web/src/App.tsx web/src/components/Layout.tsx web/src/lib/api.ts
git commit -m "feat: panel app shell, routing and api client"
```

---

## Task 3: 登录页

**Files:**
- Create: `src/pages/Login.tsx`

- [ ] **Step 1: 实现登录表单**

```tsx
export default function Login() {
  const [u, setU] = useState(""); const [p, setP] = useState("");
  return (
    <form onSubmit={async (e) => { e.preventDefault(); await api("/login", { method: "POST", body: JSON.stringify({ username: u, password: p }) }); location.href = "/overview"; }}>
      <input value={u} onChange={(e) => setU(e.target.value)} aria-label="用户名" />
      <input type="password" value={p} onChange={(e) => setP(e.target.value)} aria-label="密码" />
      <button type="submit">登录</button>
    </form>
  );
}
```

- [ ] **Step 2: 构建确认**

Run: `npm run build`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/Login.tsx
git commit -m "feat: panel login page"
```

---

## Task 4: 总览页

**Files:**
- Create: `src/pages/Overview.tsx`

- [ ] **Step 1: 实现 KPI 与服务分布**

```tsx
const kpis = [
  { label: "节点总数", value: 7, href: "/nodes" },
  { label: "在线", value: 6, href: "/nodes?status=online" },
  { label: "活跃告警", value: 2, href: "/alerts" },
  { label: "服务实例", value: 9, href: "/nodes" },
];
export default function Overview() {
  return <div className="grid grid-cols-4 gap-3">{kpis.map((k) => <Link key={k.label} to={k.href} className="kpi">{k.label}{k.value}</Link>)}</div>;
}
```

用 `recharts` 的 `BarChart` 展示服务类型分布；数据来自 `/overview`。

- [ ] **Step 2: 构建确认**

Run: `npm run build`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/Overview.tsx
git commit -m "feat: panel overview page"
```

---

## Task 5: 节点列表页

**Files:**
- Create: `src/pages/Nodes.tsx`

- [ ] **Step 1: 实现表格与过滤**

使用 `api("/nodes")` 拉取节点，表格列：名称 / IP / 服务 / 版本 / 状态 / 最后上报；状态用绿/黄/红徽章；支持 `q`、`status`、`tag` 查询参数与分页。

- [ ] **Step 2: 构建确认**

Run: `npm run build`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/Nodes.tsx
git commit -m "feat: panel nodes list page"
```

---

## Task 6: 节点详情页

**Files:**
- Create: `src/pages/NodeDetail.tsx`

- [ ] **Step 1: 实现详情与图表**

```tsx
const { id } = useParams();
// api(`/nodes/${id}`) 取主机信息与服务；api(`/nodes/${id}/metrics`) 取历史
<LineChart data={metrics}><XAxis dataKey="ts"/><YAxis/><Line dataKey="load1"/><Line dataKey="mem_used_pct"/></LineChart>
```

服务卡展示：类型、版本（独立字段）、状态、端口、证书、`tx/rx/online_clients`；流量用 `AreaChart`。

- [ ] **Step 2: 构建确认**

Run: `npm run build`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/NodeDetail.tsx
git commit -m "feat: panel node detail page"
```

---

## Task 7: 告警、版本与设置页

**Files:**
- Create: `src/pages/Alerts.tsx`、`src/pages/Versions.tsx`、`src/pages/Settings.tsx`

- [ ] **Step 1: 实现三个页面**

- 告警：表格 + 状态过滤 + ack 按钮（`POST /alerts/{id}/ack`）。
- 版本：表格展示当前/最新/来源，手动覆盖表单（`PATCH /versions/{type}`）。
- 设置：分组表单回显 `/settings` 返回的可配项。

- [ ] **Step 2: 构建确认**

Run: `npm run build`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/Alerts.tsx web/src/pages/Versions.tsx web/src/pages/Settings.tsx
git commit -m "feat: panel alerts, versions and settings pages"
```

---

## Task 8: 内嵌与 SPA 回退

**Files:**
- Modify: `cmd/net-probe-panel/main.go`（`go:embed web/dist`）
- Modify: `internal/panel/api/api.go`（静态文件与回退）

- [ ] **Step 1: 嵌入静态文件**

```go
//go:embed all:web/dist
var staticFS embed.FS
```

路由：非 `/api/` 路径优先取 `web/dist/<path>`，不存在则返回 `web/dist/index.html`。

- [ ] **Step 2: 构建确认**

Run: `go test ./... && cd web && npm run build`
Expected: 全部通过

- [ ] **Step 3: 提交**

```bash
git add cmd/net-probe-panel/main.go internal/panel/api/api.go
git commit -m "feat: embed panel frontend with spa fallback"
```

---

## Self-Review 记录

- 覆盖：脚手架/token、路由/API、登录、总览、节点列表、节点详情、告警/版本/设置、内嵌回退均映射到任务。
- 类型一致性：`api()` 客户端在 Task 2 定义，后续页面复用；路由在 Task 2 定义，页面 Task 3–7 填充。
