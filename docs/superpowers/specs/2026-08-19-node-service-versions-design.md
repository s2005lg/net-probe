# Node Service Versions Page

## Status

Approved design, pending user review.

## Goal

Replace the current global service-version list on the Versions page with a
per-node, per-service view. The page should show, for every service instance
reported by each node:

- the node it runs on;
- the service type;
- the version currently running on that node;
- the latest known version for that service type;
- whether the running service can be upgraded;
- when the latest version was fetched;
- an action column reserved for a future in-place upgrade flow.

The action column is a placeholder in this change and must not trigger any
upgrade behavior.

## Non-goals

- No upgrade execution.
- No SSH/agent command dispatch.
- No changes to the global `versions` table or the manual version-override
  API.
- No new backend endpoint.

## Data flow

The page uses two existing API endpoints:

- `GET /api/v1/admin/nodes` returns each node with its host metadata and the
  list of detected `services` from the latest report.
- `GET /api/v1/admin/versions` returns the globally known latest version for
  each supported service type.

The frontend fetches both endpoints in parallel and joins them in memory:

1. Build `Map<service_type, VersionRow>` from the versions response.
2. Iterate nodes in a stable order.
3. Iterate each node's `services` array.
4. Emit one row per reported service instance, using the matching global
   version when available.

Stable ordering is:

- node hostname ascending;
- service type ascending;
- service unit ascending (when present);
- original service order as the final tie-breaker.

## Display columns

The Versions page table contains:

| Column | Source | Notes |
| --- | --- | --- |
| 节点 | `nodeName(node)` | Prefer `host.hostname`, then `alias`, then `node_id`. |
| 服务 | `service.type` | Show `service.unit` as muted secondary text when present. |
| 当前版本 | `service.version` | Show `—` when empty. |
| 最新版本 | matching `VersionRow.latest_version` | Show `—` when no global version is known. |
| 状态 | computed | `可升级`, `已是最新`, or `未知`. |
| 更新时间 | matching `VersionRow.updated_at` | Show `—` when no global version is known. |
| 操作 | placeholder | A disabled `升级` button. |

## Upgrade status rules

The current and latest version strings are normalized by trimming leading
`v` or `V` characters.

- If either version is empty: `未知`.
- If normalized values are equal: `已是最新`.
- Otherwise split each value into numeric dotted segments and compare from
  left to right:
  - if the current version is lower: `可升级`;
  - otherwise: `已是最新`.
- Non-numeric segments are treated as `0` so common strings such as
  `v2.12.1` and `2.12.1` compare consistently.

## Action column

Every row shows an `升级` button.

- The button is disabled.
- It has no `onClick` handler and performs no network call.
- A title/tooltip states that the upgrade feature is not available yet.

## Error and empty states

- If either API request fails, show the existing full-page error text.
- If no node reports any service, show the existing empty-table message.
- If a node has no services, that node contributes no rows.

## Testing

- Extract version normalization and comparison into a small pure TypeScript
  helper so it can be unit-tested independently of React.
- The current project does not have a frontend unit-test runner configured.
  Add no new test infrastructure; verify behavior with `tsc --noEmit` and
  `npm run build`.
- Backend tests remain unchanged because no backend code is modified.
