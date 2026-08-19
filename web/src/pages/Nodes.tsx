import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import StatusBadge from "../components/StatusBadge";
import { api, nodeName, type Node, type Tag } from "../lib/api";
import { formatRelative } from "../lib/format";

const PAGE_SIZE = 10;

export default function NodesPage() {
  const [params, setParams] = useSearchParams();
  const [nodes, setNodes] = useState<Node[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [tagsLoaded, setTagsLoaded] = useState(false);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<Node | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  const q = params.get("q") ?? "";
  const status = params.get("status") ?? "";
  const tag = params.get("tag") ?? "";
  const page = Math.max(1, Number(params.get("page") ?? "1") || 1);

  useEffect(() => {
    let cancelled = false;
    api.tags()
      .then((items) => {
        if (!cancelled) {
          setTags(items);
          setTagsLoaded(true);
        }
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  useEffect(() => {
    if (!tagsLoaded) return;
    if (tag && !tags.some((item) => item.name === tag)) {
      const next = new URLSearchParams(params);
      next.delete("tag");
      next.delete("page");
      setParams(next);
    }
  }, [tag, tags, tagsLoaded, params, setParams]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api.nodes({ q, status, tag, page, page_size: PAGE_SIZE })
      .then((response) => {
        if (cancelled) return;
        setNodes(response.items);
        setTotal(response.total);
        setError("");
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [q, status, tag, page, refreshKey]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  function setParam(key: string, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    if (key !== "page") next.delete("page");
    setParams(next);
  }

  async function mute(node: Node) {
    const mutedUntil =
      node.muted_until > Math.floor(Date.now() / 1000)
        ? 0
        : Math.floor(Date.now() / 1000) + 3600;
    try {
      await api.patchNode(node.node_id, { muted_until: mutedUntil });
      setRefreshKey((key) => key + 1);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function remove(node: Node) {
    if (!window.confirm(`确认删除节点 ${nodeName(node)} 吗？`)) return;
    try {
      await api.deleteNode(node.node_id);
      setRefreshKey((key) => key + 1);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  if (error) return <p className="text-danger">{error}</p>;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <input
          value={q}
          onChange={(e) => setParam("q", e.target.value)}
          placeholder="搜索主机名 / IP / ID"
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
        <select
          value={tag}
          onChange={(e) => setParam("tag", e.target.value)}
          aria-label="标签"
          className="rounded border border-edge bg-panel px-3 py-2 text-sm text-fg outline-none focus:border-ok"
        >
          <option value="">全部标签</option>
          {tags.map((item) => (
            <option key={item.id} value={item.name}>
              {item.name}
            </option>
          ))}
        </select>
      </div>

      <div className="overflow-x-auto rounded border border-edge bg-panel">
        <table className="w-full text-left text-sm">
          <thead className="text-muted">
            <tr className="border-b border-edge">
              <th className="px-3 py-2 font-medium">名称</th>
              <th className="px-3 py-2 font-medium">IP</th>
              <th className="px-3 py-2 font-medium">IP出口地址</th>
              <th className="px-3 py-2 font-medium">服务</th>
              <th className="px-3 py-2 font-medium">版本</th>
              <th className="px-3 py-2 font-medium">状态</th>
              <th className="px-3 py-2 font-medium">最后上报</th>
              <th className="px-3 py-2 font-medium">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge">
            {nodes.map((n) => (
              <tr key={n.node_id} className="hover:bg-surface">
                <td className="px-3 py-2">
                  <Link to={`/nodes/${n.node_id}`} className="text-fg hover:text-ok">
                    {nodeName(n)}
                  </Link>
                  {n.tags.length > 0 ? (
                    <div className="mt-1 flex flex-wrap gap-1">
                      {n.tags.map((item) => (
                        <span key={item} className="rounded bg-surface px-1.5 py-0.5 text-xs text-muted">
                          {item}
                        </span>
                      ))}
                    </div>
                  ) : null}
                </td>
                <td className="px-3 py-2 text-muted">{n.host.ipv4 || n.host.ipv6 || "—"}</td>
                <td className="px-3 py-2 text-muted">{n.ip_location || "—"}</td>
                <td className="px-3 py-2 text-muted">
                  {n.services.filter((s) => s.type !== "generic").map((s) => s.type).join(", ") || "—"}
                </td>
                <td className="px-3 py-2 text-muted">
                  {n.services.filter((s) => s.type !== "generic").map((s) => s.version || s.type).join(", ") || "—"}
                </td>
                <td className="px-3 py-2">
                  <StatusBadge status={n.status} />
                </td>
                <td className="px-3 py-2 text-muted">{formatRelative(n.last_report_at)}</td>
                <td className="px-3 py-2">
                  <div className="flex gap-2">
                    <button
                      type="button"
                      onClick={() => setEditing(n)}
                      className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:border-ok hover:text-ok"
                    >
                      编辑
                    </button>
                    <button
                      type="button"
                      onClick={() => mute(n)}
                      className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:border-ok hover:text-ok"
                    >
                      {n.muted_until > Math.floor(Date.now() / 1000) ? "取消静音" : "静音 1 小时"}
                    </button>
                    <button
                      type="button"
                      onClick={() => remove(n)}
                      className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:border-danger hover:text-danger"
                    >
                      删除
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {!loading && nodes.length === 0 && (
              <tr>
                <td colSpan={8} className="px-3 py-6 text-center text-muted">
                  暂无节点
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex items-center gap-3">
        <button
          disabled={page <= 1}
          onClick={() => setParam("page", String(page - 1))}
          className="rounded border border-edge px-3 py-1 text-sm text-muted transition-colors hover:text-fg focus:outline-none disabled:opacity-40"
        >
          上一页
        </button>
        <span className="text-sm text-muted">
          {page} / {totalPages}
        </span>
        <button
          disabled={page >= totalPages}
          onClick={() => setParam("page", String(page + 1))}
          className="rounded border border-edge px-3 py-1 text-sm text-muted transition-colors hover:text-fg focus:outline-none disabled:opacity-40"
        >
          下一页
        </button>
      </div>

      {editing ? (
        <NodeEditModal
          node={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            setRefreshKey((key) => key + 1);
          }}
        />
      ) : null}
    </div>
  );
}

function NodeEditModal({
  node,
  onClose,
  onSaved,
}: {
  node: Node;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [alias, setAlias] = useState(node.alias);
  const [tagText, setTagText] = useState(node.tags.join(", "));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function save() {
    setSaving(true);
    try {
      const tags = tagText
        .split(",")
        .map((item) => item.trim())
        .filter(Boolean);
      await api.patchNode(node.node_id, { alias: alias.trim(), tags });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div className="w-full max-w-md rounded border border-edge bg-panel p-4">
        <h2 className="mb-3 font-head text-lg text-fg">编辑节点</h2>
        <label className="mb-1 block text-sm text-muted">名称 / 别名</label>
        <input
          value={alias}
          onChange={(e) => setAlias(e.target.value)}
          className="w-full rounded border border-edge bg-surface px-3 py-2 text-sm text-fg outline-none focus:border-ok"
        />
        <label className="mt-3 mb-1 block text-sm text-muted">标签（逗号分隔）</label>
        <input
          value={tagText}
          onChange={(e) => setTagText(e.target.value)}
          placeholder="例如：日本, 高防"
          className="w-full rounded border border-edge bg-surface px-3 py-2 text-sm text-fg outline-none focus:border-ok"
        />
        {error ? <p className="mt-2 text-danger">{error}</p> : null}
        <div className="mt-4 flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded border border-edge px-3 py-1.5 text-sm text-muted transition-colors hover:text-fg"
          >
            取消
          </button>
          <button
            type="button"
            disabled={saving}
            onClick={save}
            className="rounded bg-ok px-3 py-1.5 text-sm text-surface transition-opacity hover:opacity-90 disabled:opacity-50"
          >
            保存
          </button>
        </div>
      </div>
    </div>
  );
}
