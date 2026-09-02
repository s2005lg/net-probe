import { Copy, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import StatusBadge from "../components/StatusBadge";
import { api, nodeName, type Enrollment, type Node, type Release, type SessionUser, type Tag } from "../lib/api";
import { formatRelative, formatTime } from "../lib/format";
import { egressIP } from "../lib/host";

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
  const [actor, setActor] = useState<SessionUser | null>(null);
  const [releases, setReleases] = useState<Release[]>([]);
  const [addingAgent, setAddingAgent] = useState(false);
  const [enrollment, setEnrollment] = useState<Enrollment>();
  const [enrollmentLabel, setEnrollmentLabel] = useState("");
  const [agentPlatform, setAgentPlatform] = useState("linux/amd64");
  const [enrollmentBusy, setEnrollmentBusy] = useState(false);
  const [releaseTag, setReleaseTag] = useState("");
  const [releaseBusy, setReleaseBusy] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [view, setView] = useState<"table" | "cards">("table");

  const q = params.get("q") ?? "";
  const status = params.get("status") ?? "";
  const tag = params.get("tag") ?? "";
  const page = Math.max(1, Number(params.get("page") ?? "1") || 1);

  useEffect(() => {
    let cancelled = false;
    Promise.all([api.tags(), api.me(), api.releases()])
      .then(([items, me, releaseResponse]) => {
        if (!cancelled) {
          setTags(items);
          setActor(me);
          setReleases(releaseResponse.items);
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

  async function createEnrollment() {
    setEnrollmentBusy(true);
    setError("");
    try {
      setEnrollment(await api.createEnrollment(enrollmentLabel.trim()));
      setAddingAgent(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setEnrollmentBusy(false);
    }
  }

  async function importRelease() {
    const [os, arch] = agentPlatform.split("/");
    setReleaseBusy(true);
    setError("");
    try {
      const imported = await api.importGitHubRelease({ version: releaseTag.trim(), os, arch });
      setReleases((items) => [imported, ...items.filter((item) => item.id !== imported.id)]);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setReleaseBusy(false);
    }
  }

  if (error) return <p className="text-danger">{error}</p>;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <AddAgentPanel
          actor={actor}
          releases={releases}
          enrollment={enrollment}
          label={enrollmentLabel}
          platform={agentPlatform}
          open={addingAgent}
          busy={enrollmentBusy}
          releaseTag={releaseTag}
          releaseBusy={releaseBusy}
          onOpen={() => setAddingAgent(true)}
          onClose={() => setAddingAgent(false)}
          onLabelChange={setEnrollmentLabel}
          onPlatformChange={setAgentPlatform}
          onReleaseTagChange={setReleaseTag}
          onImportRelease={() => void importRelease()}
          onCreate={() => void createEnrollment()}
        />
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
        <div className="ml-auto flex overflow-hidden rounded border border-edge">
          <button
            type="button"
            onClick={() => setView("table")}
            className={`px-3 py-2 text-sm transition-colors ${view === "table" ? "bg-surface text-fg" : "bg-panel text-muted hover:text-fg"}`}
          >
            表格
          </button>
          <button
            type="button"
            onClick={() => setView("cards")}
            className={`px-3 py-2 text-sm transition-colors ${view === "cards" ? "bg-surface text-fg" : "bg-panel text-muted hover:text-fg"}`}
          >
            卡片
          </button>
        </div>
      </div>

      {view === "table" ? (
        <div className="overflow-x-auto rounded border border-edge bg-panel">
        <table className="w-full text-left text-sm">
          <thead className="text-muted">
            <tr className="border-b border-edge">
              <th className="px-3 py-2 font-medium">名称</th>
              <th className="px-3 py-2 font-medium">出口 IP</th>
              <th className="px-3 py-2 font-medium">国家/地区</th>
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
                <td className="px-3 py-2 text-muted">{egressIP(n.host, n.effective_ip)}</td>
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
                  <NodeActions
                    node={n}
                    onEdit={() => setEditing(n)}
                    onMute={() => mute(n)}
                    onRemove={() => remove(n)}
                  />
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
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {nodes.map((n) => (
            <div key={n.node_id} className="rounded border border-edge bg-panel p-3">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <Link to={`/nodes/${n.node_id}`} className="font-medium text-fg hover:text-ok">
                    {nodeName(n)}
                  </Link>
                  <div className="mt-0.5 truncate text-xs text-muted">
                    {egressIP(n.host, n.effective_ip)}
                  </div>
                </div>
                <StatusBadge status={n.status} />
              </div>
              {n.tags.length > 0 ? (
                <div className="mt-2 flex flex-wrap gap-1">
                  {n.tags.map((item) => (
                    <span key={item} className="rounded bg-surface px-1.5 py-0.5 text-xs text-muted">
                      {item}
                    </span>
                  ))}
                </div>
              ) : null}
              <dl className="mt-3 space-y-1 text-sm text-muted">
                <div className="flex justify-between gap-2">
                  <dt>出口 IP</dt>
                  <dd className="truncate text-fg">{egressIP(n.host, n.effective_ip)}</dd>
                </div>
                <div className="flex justify-between gap-2">
                  <dt>国家/地区</dt>
                  <dd className="truncate text-fg">{n.ip_location || "—"}</dd>
                </div>
                <div className="flex justify-between gap-2">
                  <dt>服务</dt>
                  <dd className="truncate text-fg">
                    {n.services.filter((s) => s.type !== "generic").map((s) => s.type).join(", ") || "—"}
                  </dd>
                </div>
                <div className="flex justify-between gap-2">
                  <dt>版本</dt>
                  <dd className="truncate text-fg">
                    {n.services.filter((s) => s.type !== "generic").map((s) => s.version || s.type).join(", ") || "—"}
                  </dd>
                </div>
                <div className="flex justify-between gap-2">
                  <dt>最后上报</dt>
                  <dd className="truncate text-fg">{formatRelative(n.last_report_at)}</dd>
                </div>
              </dl>
              <div className="mt-3">
                <NodeActions
                  node={n}
                  onEdit={() => setEditing(n)}
                  onMute={() => mute(n)}
                  onRemove={() => remove(n)}
                />
              </div>
            </div>
          ))}
          {!loading && nodes.length === 0 && (
            <p className="col-span-full py-6 text-center text-muted">暂无节点</p>
          )}
        </div>
      )}

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

export function AddAgentPanel({
  actor,
  releases = [],
  enrollment,
  label = "",
  platform = "linux/amd64",
  open = false,
  busy = false,
  releaseTag = "",
  releaseBusy = false,
  panelOrigin = typeof window === "undefined" ? "https://panel.example.invalid" : window.location.origin,
  onOpen,
  onClose,
  onLabelChange,
  onPlatformChange,
  onReleaseTagChange,
  onImportRelease,
  onCreate,
}: {
  actor: SessionUser | null;
  releases?: Release[];
  enrollment?: Enrollment;
  label?: string;
  platform?: string;
  open?: boolean;
  busy?: boolean;
  releaseTag?: string;
  releaseBusy?: boolean;
  panelOrigin?: string;
  onOpen?: () => void;
  onClose?: () => void;
  onLabelChange?: (value: string) => void;
  onPlatformChange?: (value: string) => void;
  onReleaseTagChange?: (value: string) => void;
  onImportRelease?: () => void;
  onCreate?: () => void;
}) {
  if (actor?.role !== "admin") return null;
  const [os, arch] = platform.split("/");
  const release = releases.find((item) => item.manifest.os === os && item.manifest.arch === arch);
  const command = enrollment && release ? buildInstallCommand(panelOrigin, release.manifest.version, enrollment) : "";
  const canCreateEnrollment = Boolean(release) && !busy;

  return (
    <>
      <button
        type="button"
        onClick={onOpen}
        className="inline-flex min-h-11 cursor-pointer items-center gap-2 rounded bg-ok px-3 py-2 text-sm font-medium text-surface transition-opacity hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok"
      >
        <Plus size={16} aria-hidden="true" />
        添加 Agent
      </button>
      {open || enrollment ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="w-full max-w-2xl rounded border border-edge bg-panel p-4">
            <div className="flex items-start justify-between gap-3">
              <div>
                <h2 className="font-head text-lg text-fg">添加 Agent</h2>
                <p className="mt-1 text-sm text-muted">选择平台，导入签名发布，再生成一次性安装命令。</p>
              </div>
              <button type="button" onClick={onClose} className="rounded border border-edge px-3 py-1.5 text-sm text-muted hover:text-fg">
                关闭
              </button>
            </div>
            <ol className="mt-4 space-y-4">
              <li>
                <p className="text-sm font-medium text-fg">1. 选择平台和 Release</p>
                <div className="mt-2 grid gap-3 sm:grid-cols-[180px_1fr_auto]">
                  <label className="text-sm text-muted">
                    平台
                    <select
                      value={platform}
                      onChange={(event) => onPlatformChange?.(event.target.value)}
                      className="mt-1 min-h-11 w-full rounded border border-edge bg-surface px-3 py-2 text-fg outline-none focus-visible:ring-2 focus-visible:ring-ok"
                    >
                      <option value="linux/amd64">linux/amd64</option>
                      <option value="linux/arm64">linux/arm64</option>
                    </select>
                  </label>
                  <label className="text-sm text-muted">
                    GitHub Release tag
                    <input
                      value={releaseTag}
                      placeholder={release?.manifest.version ?? "v0.1.1"}
                      onChange={(event) => onReleaseTagChange?.(event.target.value)}
                      className="mt-1 min-h-11 w-full rounded border border-edge bg-surface px-3 py-2 text-fg outline-none focus-visible:ring-2 focus-visible:ring-ok"
                    />
                  </label>
                  <button
                    type="button"
                    disabled={releaseBusy || !releaseTag.trim()}
                    onClick={onImportRelease}
                    className="min-h-11 cursor-pointer self-end rounded border border-edge px-4 py-2 text-sm text-fg hover:border-ok disabled:cursor-wait disabled:opacity-50"
                  >
                    {releaseBusy ? "导入中…" : "导入签名发布"}
                  </button>
                </div>
                {release ? (
                  <p className="mt-2 text-sm text-ok">已选择 {release.manifest.version} / {release.manifest.os}/{release.manifest.arch}</p>
                ) : (
                  <p className="mt-2 text-sm text-warn">尚未导入此平台的签名发布，不能生成安装命令。</p>
                )}
              </li>
              <li>
                <p className="text-sm font-medium text-fg">2. 生成一次性注册码</p>
                <div className="mt-2 grid gap-3 sm:grid-cols-[1fr_auto]">
                  <label className="text-sm text-muted">
                    标签（可选）
                    <input
                      value={label}
                      maxLength={256}
                      onChange={(event) => onLabelChange?.(event.target.value)}
                      className="mt-1 min-h-11 w-full rounded border border-edge bg-surface px-3 py-2 text-fg outline-none focus-visible:ring-2 focus-visible:ring-ok"
                    />
                  </label>
                  <button
                    type="button"
                    disabled={!canCreateEnrollment}
                    onClick={onCreate}
                    className="min-h-11 cursor-pointer self-end rounded bg-ok px-4 py-2 text-sm font-medium text-surface hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {busy ? "生成中…" : "生成安装命令"}
                  </button>
                </div>
              </li>
              <li>
                <p className="text-sm font-medium text-fg">3. 在新 VPS 执行</p>
                {enrollment && command ? (
                  <div className="mt-2">
                    <p className="text-sm text-ok">有效至 {formatTime(enrollment.expires_at)}</p>
                    <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-all rounded bg-surface p-3 font-head text-xs leading-5 text-fg">{command}</pre>
                    <button
                      type="button"
                      onClick={() => void navigator.clipboard?.writeText(command)}
                      className="mt-2 inline-flex min-h-11 cursor-pointer items-center gap-2 rounded border border-edge px-3 py-2 text-sm text-fg hover:border-ok focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok"
                    >
                      <Copy size={16} aria-hidden="true" />
                      复制安装命令
                    </button>
                    <p className="mt-2 text-xs text-muted">卸载命令：curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/main/uninstall.sh | sudo bash</p>
                  </div>
                ) : (
                  <p className="mt-2 text-sm text-muted">生成后会显示完整命令。</p>
                )}
              </li>
            </ol>
          </div>
        </div>
      ) : null}
    </>
  );
}

function buildInstallCommand(origin: string, version: string, enrollment: Enrollment): string {
  return [
    `curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/${version}/install.sh | env \\`,
    `  NET_PROBE_PANEL_URL=${shellQuote(origin)} \\`,
    `  NET_PROBE_VERSION=${shellQuote(version)} \\`,
    `  NET_PROBE_CA_FINGERPRINT=${shellQuote(enrollment.ca_fingerprint)} \\`,
    `  NET_PROBE_ENROLLMENT_CODE=${shellQuote(enrollment.code)} \\`,
    `  NET_PROBE_RELEASE_PUBLIC_KEY_HEX=${shellQuote(enrollment.release_public_key_hex)} \\`,
    "  bash",
  ].join("\n");
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

function NodeActions({
  node,
  onEdit,
  onMute,
  onRemove,
}: {
  node: Node;
  onEdit: () => void;
  onMute: () => void;
  onRemove: () => void;
}) {
  return (
    <div className="flex gap-2">
      <button
        type="button"
        onClick={onEdit}
        className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:border-ok hover:text-ok"
      >
        编辑
      </button>
      <button
        type="button"
        onClick={onMute}
        className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:border-ok hover:text-ok"
      >
        {node.muted_until > Math.floor(Date.now() / 1000) ? "取消静音" : "静音 1 小时"}
      </button>
      <button
        type="button"
        onClick={onRemove}
        className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:border-danger hover:text-danger"
      >
        删除
      </button>
    </div>
  );
}
