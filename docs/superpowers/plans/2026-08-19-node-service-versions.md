# Node Service Versions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Versions page global list with a per-node, per-service table that shows current version, latest version, upgrade status, and a disabled upgrade action.

**Architecture:** Keep all backend APIs unchanged. The React page fetches `/nodes` and `/versions`, joins them in memory, and computes upgrade status with a pure version helper.

**Tech Stack:** React 18, TypeScript 5.6, Tailwind CSS 4, Recharts 2.

## Global Constraints

- Do not modify backend code or the database schema.
- Do not implement any upgrade action; the button must be disabled with no click handler.
- Use the existing `nodeName(node)` helper from `web/src/lib/api.ts`.
- Keep the existing table styling and empty/error states.
- Verification commands: `npx tsc --noEmit` and `npm run build`, run from `web/`.

---

## File Structure

- Create: `web/src/lib/version.ts`
  - Pure version normalization and upgrade-status logic.
- Modify: `web/src/components/StatusBadge.tsx`
  - Add visual labels/colors for `upgradable`, `latest`, and `unknown`.
- Modify: `web/src/pages/Versions.tsx`
  - Fetch nodes and versions, build the 7-column node-service table.

---

### Task 1: Add pure version status helper

**Files:**
- Create: `web/src/lib/version.ts`

**Interfaces:**
- Produces: `export type UpgradeStatus = "upgradable" | "latest" | "unknown"`
- Produces: `export function upgradeStatus(current: string, latest: string): UpgradeStatus`

- [ ] **Step 1: Create `web/src/lib/version.ts`**

```ts
export type UpgradeStatus = "upgradable" | "latest" | "unknown";

function normalize(version: string): string {
  return version.trim().replace(/^[vV]+/, "");
}

function numericSegments(version: string): number[] {
  return normalize(version)
    .split(".")
    .map((part) => {
      const parsed = Number.parseInt(part, 10);
      return Number.isFinite(parsed) ? parsed : 0;
    });
}

export function upgradeStatus(current: string, latest: string): UpgradeStatus {
  if (!current || !latest) {
    return "unknown";
  }

  const currentNormalized = normalize(current);
  const latestNormalized = normalize(latest);
  if (currentNormalized === latestNormalized) {
    return "latest";
  }

  const currentSegments = numericSegments(current);
  const latestSegments = numericSegments(latest);
  const length = Math.max(currentSegments.length, latestSegments.length);

  for (let i = 0; i < length; i += 1) {
    const currentPart = currentSegments[i] ?? 0;
    const latestPart = latestSegments[i] ?? 0;
    if (currentPart < latestPart) {
      return "upgradable";
    }
    if (currentPart > latestPart) {
      return "latest";
    }
  }

  return "latest";
}
```

- [ ] **Step 2: Verify the file type-checks**

Run:

```bash
cd web && npx tsc --noEmit
```

Expected: command exits `0` with no output.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/version.ts
git commit -m "feat: add version status comparison helper"
```

---

### Task 2: Add version status labels to StatusBadge

**Files:**
- Modify: `web/src/components/StatusBadge.tsx`

**Interfaces:**
- Consumes: status string values `upgradable`, `latest`, and `unknown` from Task 1.
- Produces: no new exported symbols.

- [ ] **Step 1: Update `MAP` in `web/src/components/StatusBadge.tsx`**

Replace:

```ts
const MAP: Record<string, { color: string; label: string }> = {
  online: { color: "var(--color-ok)", label: "在线" },
  offline: { color: "var(--color-danger)", label: "离线" },
  firing: { color: "var(--color-danger)", label: "告警中" },
  recovered: { color: "var(--color-warn)", label: "已恢复" },
  acknowledged: { color: "var(--color-muted)", label: "已确认" },
};
```

with:

```ts
const MAP: Record<string, { color: string; label: string }> = {
  online: { color: "var(--color-ok)", label: "在线" },
  offline: { color: "var(--color-danger)", label: "离线" },
  firing: { color: "var(--color-danger)", label: "告警中" },
  recovered: { color: "var(--color-warn)", label: "已恢复" },
  acknowledged: { color: "var(--color-muted)", label: "已确认" },
  upgradable: { color: "var(--color-warn)", label: "可升级" },
  latest: { color: "var(--color-ok)", label: "已是最新" },
  unknown: { color: "var(--color-muted)", label: "未知" },
};
```

- [ ] **Step 2: Verify the file type-checks**

Run:

```bash
cd web && npx tsc --noEmit
```

Expected: command exits `0` with no output.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/StatusBadge.tsx
git commit -m "feat: add version status badges"
```

---

### Task 3: Replace Versions page with per-node service table

**Files:**
- Modify: `web/src/pages/Versions.tsx`

**Interfaces:**
- Consumes:
  - `api.nodes(): Promise<Node[]>`
  - `api.versions(): Promise<VersionRow[]>`
  - `nodeName(node: Node): string` from `web/src/lib/api.ts`
  - `upgradeStatus(current: string, latest: string): UpgradeStatus` from `web/src/lib/version.ts`
  - `formatTime(ts: number): string` from `web/src/lib/format.ts`
  - `StatusBadge` from `web/src/components/StatusBadge.tsx`
- Produces: no new exported symbols.

- [ ] **Step 1: Replace the full contents of `web/src/pages/Versions.tsx`**

```tsx
import { useEffect, useState } from "react";
import StatusBadge from "../components/StatusBadge";
import { api, nodeName, type Node, type Service, type VersionRow } from "../lib/api";
import { formatTime } from "../lib/format";
import { upgradeStatus, type UpgradeStatus } from "../lib/version";

type NodeServiceVersionRow = {
  node: Node;
  service: Service;
  latest: VersionRow | undefined;
  status: UpgradeStatus;
};

export default function VersionsPage() {
  const [rows, setRows] = useState<NodeServiceVersionRow[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;

    Promise.all([api.nodes(), api.versions()])
      .then(([nodes, versions]) => {
        if (cancelled) return;

        const versionByType = new Map(
          versions.map((version) => [version.service_type, version]),
        );
        const nextRows = nodes
          .flatMap((node) =>
            node.services.map((service) => {
              const latest = versionByType.get(service.type);
              return {
                node,
                service,
                latest,
                status: upgradeStatus(
                  service.version ?? "",
                  latest?.latest_version ?? "",
                ),
              };
            }),
          )
          .sort((a, b) => {
            const byNode = nodeName(a.node).localeCompare(nodeName(b.node));
            if (byNode !== 0) return byNode;

            const byType = a.service.type.localeCompare(b.service.type);
            if (byType !== 0) return byType;

            return (a.service.unit ?? "").localeCompare(b.service.unit ?? "");
          });

        setRows(nextRows);
        setError("");
      })
      .catch((e) => {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : String(e));
        }
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="space-y-4">
      {error && <p className="text-danger">{error}</p>}
      <div className="overflow-x-auto rounded border border-edge bg-panel">
        <table className="w-full text-left text-sm">
          <thead className="text-muted">
            <tr className="border-b border-edge">
              <th className="px-3 py-2 font-medium">节点</th>
              <th className="px-3 py-2 font-medium">服务</th>
              <th className="px-3 py-2 font-medium">当前版本</th>
              <th className="px-3 py-2 font-medium">最新版本</th>
              <th className="px-3 py-2 font-medium">状态</th>
              <th className="px-3 py-2 font-medium">更新时间</th>
              <th className="px-3 py-2 font-medium">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge">
            {rows.map((row) => (
              <tr
                key={`${row.node.node_id}-${row.service.type}-${row.service.unit ?? row.service.binary ?? row.service.main_pid ?? ""}`}
                className="hover:bg-surface"
              >
                <td className="px-3 py-2 text-fg">{nodeName(row.node)}</td>
                <td className="px-3 py-2 text-fg">
                  <div>{row.service.type}</div>
                  {row.service.unit ? (
                    <div className="text-xs text-muted">{row.service.unit}</div>
                  ) : null}
                </td>
                <td className="px-3 py-2 text-fg">{row.service.version || "—"}</td>
                <td className="px-3 py-2 text-fg">{row.latest?.latest_version || "—"}</td>
                <td className="px-3 py-2">
                  <StatusBadge status={row.status} />
                </td>
                <td className="px-3 py-2 text-muted">
                  {row.latest ? formatTime(row.latest.updated_at) : "—"}
                </td>
                <td className="px-3 py-2">
                  <button
                    type="button"
                    disabled
                    title="升级功能暂未开放"
                    className="rounded border border-edge px-2 py-1 text-xs text-muted opacity-40"
                  >
                    升级
                  </button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-muted">
                  暂无节点服务版本数据
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Run TypeScript check**

Run:

```bash
cd web && npx tsc --noEmit
```

Expected: command exits `0` with no output.

- [ ] **Step 3: Run production frontend build**

Run:

```bash
cd web && npm run build
```

Expected: Vite build completes with a `built in ...` message.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/Versions.tsx
git commit -m "feat: show per-node service versions"
```

---

## Self-Review

- Spec coverage: node, service, current version, latest version, status, updated time, and disabled upgrade action are all present in Task 3.
- No placeholders remain in code blocks.
- Type names are consistent across tasks: `UpgradeStatus`, `upgradeStatus`, `nodeName`, `formatTime`, `VersionRow`, `Service`, and `Node`.
