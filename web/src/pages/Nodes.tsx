import { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import StatusBadge from "../components/StatusBadge";
import { api, type Node } from "../lib/api";
import { formatRelative } from "../lib/format";

const PAGE_SIZE = 10;

export default function NodesPage() {
  const [nodes, setNodes] = useState<Node[]>([]);
  const [error, setError] = useState("");
  const [params, setParams] = useSearchParams();
  const [page, setPage] = useState(1);
  const q = params.get("q") ?? "";
  const status = params.get("status") ?? "";

  useEffect(() => {
    api.nodes().then(setNodes).catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  const filtered = useMemo(
    () =>
      nodes.filter((n) => {
        const text = `${n.alias} ${n.node_id} ${n.host.ipv4 ?? ""} ${n.host.ipv6 ?? ""}`.toLowerCase();
        if (q && !text.includes(q.toLowerCase())) return false;
        if (status && n.status !== status) return false;
        return true;
      }),
    [nodes, q, status],
  );

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const pageItems = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);

  function setParam(key: string, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
    setPage(1);
  }

  if (error) return <p className="text-danger">{error}</p>;

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <input
          value={q}
          onChange={(e) => setParam("q", e.target.value)}
          placeholder="搜索名称 / IP"
          aria-label="搜索"
          className="w-64 rounded border border-edge bg-panel px-3 py-2 text-sm text-fg outline-none focus:border-ok"
        />
        <select
          value={status}
          onChange={(e) => setParam("status", e.target.value)}
          aria-label="状态"
          className="rounded border border-edge bg-panel px-3 py-2 text-sm text-fg outline-none focus:border-ok"
        >
          <option value="">全部状态</option>
          <option value="online">在线</option>
          <option value="offline">离线</option>
        </select>
      </div>

      <div className="overflow-x-auto rounded border border-edge bg-panel">
        <table className="w-full text-left text-sm">
          <thead className="text-muted">
            <tr className="border-b border-edge">
              <th className="px-3 py-2 font-medium">名称</th>
              <th className="px-3 py-2 font-medium">IP</th>
              <th className="px-3 py-2 font-medium">服务</th>
              <th className="px-3 py-2 font-medium">版本</th>
              <th className="px-3 py-2 font-medium">状态</th>
              <th className="px-3 py-2 font-medium">最后上报</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge">
            {pageItems.map((n) => (
              <tr key={n.node_id} className="hover:bg-surface">
                <td className="px-3 py-2">
                  <Link to={`/nodes/${n.node_id}`} className="text-fg hover:text-ok">
                    {n.alias || n.node_id}
                  </Link>
                </td>
                <td className="px-3 py-2 text-muted">{n.host.ipv4 || n.host.ipv6 || "—"}</td>
                <td className="px-3 py-2 text-muted">
                  {n.services.map((s) => s.type).join(", ") || "—"}
                </td>
                <td className="px-3 py-2 text-muted">
                  {n.services.map((s) => s.version || s.type).join(", ") || "—"}
                </td>
                <td className="px-3 py-2">
                  <StatusBadge status={n.status} />
                </td>
                <td className="px-3 py-2 text-muted">{formatRelative(n.last_report_at)}</td>
              </tr>
            ))}
            {pageItems.length === 0 && (
              <tr>
                <td colSpan={6} className="px-3 py-6 text-center text-muted">
                  暂无节点
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {totalPages > 1 && (
        <div className="flex items-center gap-3">
          <button
            disabled={page <= 1}
            onClick={() => setPage(page - 1)}
            className="rounded border border-edge px-3 py-1 text-sm text-muted transition-colors hover:text-fg focus:outline-none disabled:opacity-40"
          >
            上一页
          </button>
          <span className="text-sm text-muted">
            {page} / {totalPages}
          </span>
          <button
            disabled={page >= totalPages}
            onClick={() => setPage(page + 1)}
            className="rounded border border-edge px-3 py-1 text-sm text-muted transition-colors hover:text-fg focus:outline-none disabled:opacity-40"
          >
            下一页
          </button>
        </div>
      )}
    </div>
  );
}
