import { CheckCircle2, Copy, KeyRound, ShieldCheck, UploadCloud } from "lucide-react";
import { FormEvent, useEffect, useMemo, useState } from "react";
import {
  agentFromNode,
  api,
  type AgentIdentity,
  type Enrollment,
  type Release,
  type ReleaseManifest,
  type SessionUser,
} from "../lib/api";
import { formatBytes, formatRelative, formatTime } from "../lib/format";

export default function AgentsPage() {
  const [actor, setActor] = useState<SessionUser | null>(null);
  const [agents, setAgents] = useState<AgentIdentity[]>([]);
  const [releases, setReleases] = useState<Release[]>([]);
  const [enrollment, setEnrollment] = useState<Enrollment>();
  const [selectedAgentIDs, setSelectedAgentIDs] = useState<string[]>([]);
  const [selectedReleaseID, setSelectedReleaseID] = useState<number>();
  const [versionConfirmation, setVersionConfirmation] = useState("");
  const [label, setLabel] = useState("");
  const [manifestText, setManifestText] = useState("");
  const [signature, setSignature] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    const [me, nodes, releaseResponse] = await Promise.all([api.me(), api.allNodes(), api.releases()]);
    setActor(me);
    setAgents(nodes.map(agentFromNode).filter((item): item is AgentIdentity => item !== null));
    setReleases(releaseResponse.items);
    setSelectedReleaseID((current) => current ?? releaseResponse.items[0]?.id);
  }

  useEffect(() => {
    load().catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  }, []);

  useEffect(() => {
    const requested = new URLSearchParams(window.location.search).get("agent");
    if (requested) setSelectedAgentIDs([requested]);
  }, []);

  if (!actor) {
    return <p className={error ? "text-danger" : "text-muted"}>{error || "加载中…"}</p>;
  }

  async function createEnrollment() {
    setBusy("enrollment");
    setError("");
    setNotice("");
    try {
      setEnrollment(await api.createEnrollment(label.trim()));
      setNotice("一次性注册码已创建，请在过期前使用");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy("");
    }
  }

  async function importRelease(event: FormEvent) {
    event.preventDefault();
    setBusy("release");
    setError("");
    setNotice("");
    try {
      const manifest = JSON.parse(manifestText) as ReleaseManifest;
      await api.importRelease(manifest, signature.trim());
      const response = await api.releases();
      setReleases(response.items);
      setSelectedReleaseID(response.items[0]?.id);
      setManifestText("");
      setSignature("");
      setNotice("签名发布已验证并导入");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "发布内容不是有效 JSON");
    } finally {
      setBusy("");
    }
  }

  async function reauthenticate() {
    setBusy("reauth");
    setError("");
    try {
      await api.reauthenticate(password);
      const me = await api.me();
      setActor(me);
      setPassword("");
      setNotice("密码验证成功，10 分钟内可发起升级");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy("");
    }
  }

  async function createUpgrades(event: FormEvent) {
    event.preventDefault();
    const release = releases.find((item) => item.id === selectedReleaseID);
    if (!release) return;
    setBusy("upgrade");
    setError("");
    setNotice("");
    try {
      await api.createUpgrades({
        version: release.manifest.version,
        os: release.manifest.os,
        arch: release.manifest.arch,
        agent_ids: selectedAgentIDs,
        confirmed: true,
      });
      setNotice(`已为 ${selectedAgentIDs.length} 个 Agent 原子创建升级命令`);
      setVersionConfirmation("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy("");
    }
  }

  return (
    <AgentsView
      actor={actor}
      agents={agents}
      releases={releases}
      enrollment={enrollment}
      selectedAgentIDs={selectedAgentIDs}
      selectedReleaseID={selectedReleaseID}
      versionConfirmation={versionConfirmation}
      enrollmentLabel={label}
      manifestText={manifestText}
      signature={signature}
      password={password}
      busy={busy}
      error={error}
      notice={notice}
      onEnrollmentLabelChange={setLabel}
      onCreateEnrollment={() => void createEnrollment()}
      onManifestTextChange={setManifestText}
      onSignatureChange={setSignature}
      onImportRelease={importRelease}
      onSelectedReleaseIDChange={(value) => {
        setSelectedReleaseID(value);
        setSelectedAgentIDs([]);
        setVersionConfirmation("");
      }}
      onAgentSelectionChange={setSelectedAgentIDs}
      onVersionConfirmationChange={setVersionConfirmation}
      onPasswordChange={setPassword}
      onReauthenticate={() => void reauthenticate()}
      onCreateUpgrades={createUpgrades}
    />
  );
}

export interface AgentsViewProps {
  actor: SessionUser;
  agents: AgentIdentity[];
  releases: Release[];
  enrollment?: Enrollment;
  selectedAgentIDs?: string[];
  selectedReleaseID?: number;
  versionConfirmation?: string;
  enrollmentLabel?: string;
  manifestText?: string;
  signature?: string;
  password?: string;
  busy?: string;
  error?: string;
  notice?: string;
  panelOrigin?: string;
  onEnrollmentLabelChange?: (value: string) => void;
  onCreateEnrollment?: () => void;
  onManifestTextChange?: (value: string) => void;
  onSignatureChange?: (value: string) => void;
  onImportRelease?: (event: FormEvent) => void;
  onSelectedReleaseIDChange?: (value: number) => void;
  onAgentSelectionChange?: (value: string[]) => void;
  onVersionConfirmationChange?: (value: string) => void;
  onPasswordChange?: (value: string) => void;
  onReauthenticate?: () => void;
  onCreateUpgrades?: (event: FormEvent) => void;
}

export function AgentsView({
  actor,
  agents,
  releases,
  enrollment,
  selectedAgentIDs = [],
  selectedReleaseID,
  versionConfirmation = "",
  enrollmentLabel = "",
  manifestText = "",
  signature = "",
  password = "",
  busy = "",
  error = "",
  notice = "",
  panelOrigin = typeof window === "undefined" ? "https://panel.example.invalid" : window.location.origin,
  onEnrollmentLabelChange,
  onCreateEnrollment,
  onManifestTextChange,
  onSignatureChange,
  onImportRelease,
  onSelectedReleaseIDChange,
  onAgentSelectionChange,
  onVersionConfirmationChange,
  onPasswordChange,
  onReauthenticate,
  onCreateUpgrades,
}: AgentsViewProps) {
  const selectedRelease = releases.find((item) => item.id === selectedReleaseID) ?? releases[0];
  const effectiveReleaseID = selectedReleaseID ?? selectedRelease?.id;
  const recentReauth = isRecentReauthentication(actor.reauthenticated_at);
  const compatibleAgents = useMemo(
    () => agents.filter((agent) => isCompatible(agent, selectedRelease)),
    [agents, selectedRelease],
  );
  const validTargets = selectedAgentIDs.length > 0 && selectedAgentIDs.every((id) => compatibleAgents.some((agent) => agent.agent_id === id));
  const readyToUpgrade =
    actor.role === "admin" &&
    recentReauth &&
    Boolean(selectedRelease) &&
    validTargets &&
    versionConfirmation === selectedRelease?.manifest.version;
  const installVersion = selectedRelease?.manifest.version ?? "<explicit-signed-release>";
  const installCommand = enrollment
    ? buildInstallCommand(panelOrigin, installVersion, enrollment)
    : "";

  function toggleAgent(agentID: string, checked: boolean) {
    if (checked && selectedAgentIDs.length >= 100) return;
    const next = checked
      ? Array.from(new Set([...selectedAgentIDs, agentID]))
      : selectedAgentIDs.filter((value) => value !== agentID);
    onAgentSelectionChange?.(next);
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-head text-2xl text-fg">Agent 管理</h1>
        <p className="mt-1 text-sm text-muted">注册、证书状态、签名发布和批量升级</p>
      </div>

      <div className="min-h-5 text-sm" aria-live="polite">
        {error ? <span className="text-danger">{error}</span> : null}
        {!error && notice ? <span className="text-ok">{notice}</span> : null}
      </div>

      <section className="rounded border border-edge bg-panel p-4" aria-labelledby="agent-inventory-title">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 id="agent-inventory-title" className="font-head text-fg">Agent 清单</h2>
            <p className="mt-1 text-xs text-muted">遥测在线与控制通道在线是两种独立状态</p>
          </div>
          <span className="text-sm text-muted">{agents.length} 个身份</span>
        </div>
        <div className="mt-3 overflow-x-auto rounded border border-edge">
          <table className="w-full min-w-[900px] text-left text-sm">
            <thead className="bg-surface text-muted">
              <tr>
                <th className="px-3 py-2 font-medium">节点</th>
                <th className="px-3 py-2 font-medium">控制通道</th>
                <th className="px-3 py-2 font-medium">Agent</th>
                <th className="px-3 py-2 font-medium">平台</th>
                <th className="px-3 py-2 font-medium">证书到期</th>
                <th className="px-3 py-2 font-medium">能力</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge">
              {agents.map((agent) => (
                <tr key={agent.agent_id}>
                  <td className="px-3 py-2 text-fg">
                    <div>{agent.display_name}</div>
                    <div className="font-head text-xs text-muted">{agent.node_id}</div>
                  </td>
                  <td className="px-3 py-2">
                    <span className={agent.control_status === "online" ? "text-ok" : "text-muted"}>
                      {agent.control_status === "online" ? "在线" : "离线"}
                    </span>
                    <div className="text-xs text-muted">
                      {agent.last_heartbeat_at ? formatRelative(agent.last_heartbeat_at) : "尚无心跳"}
                    </div>
                  </td>
                  <td className="px-3 py-2 text-fg">{agent.agent_version || "—"}</td>
                  <td className="px-3 py-2 text-muted">{[agent.os, agent.arch].filter(Boolean).join(" / ") || "—"}</td>
                  <td className="px-3 py-2 text-muted">
                    {agent.certificate_expires_at ? formatTime(agent.certificate_expires_at) : "—"}
                  </td>
                  <td className="px-3 py-2 text-muted">{agent.capabilities.map(capabilityLabel).join("、") || "—"}</td>
                </tr>
              ))}
              {agents.length === 0 ? (
                <tr><td colSpan={6} className="px-3 py-6 text-center text-muted">暂无已注册 Agent</td></tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>

      {actor.role === "admin" ? (
        <>
          <section className="rounded border border-edge bg-panel p-4" aria-labelledby="enrollment-title">
            <div className="flex items-center gap-2">
              <KeyRound size={18} className="text-ok" aria-hidden="true" />
              <h2 id="enrollment-title" className="font-head text-fg">创建一次性注册码</h2>
            </div>
            <p className="mt-1 text-sm text-muted">注册码仅显示一次，10 分钟过期，成功注册后立即失效。</p>
            <div className="mt-3 flex flex-col gap-2 sm:flex-row">
              <label className="flex-1 text-sm text-muted">
                标签（可选）
                <input
                  value={enrollmentLabel}
                  maxLength={256}
                  onChange={(event) => onEnrollmentLabelChange?.(event.target.value)}
                  className="mt-1 min-h-11 w-full rounded border border-edge bg-surface px-3 py-2 text-fg outline-none focus-visible:ring-2 focus-visible:ring-ok"
                />
              </label>
              <button
                type="button"
                disabled={busy === "enrollment"}
                onClick={onCreateEnrollment}
                className="min-h-11 cursor-pointer self-end rounded bg-ok px-4 py-2 text-sm font-medium text-surface transition-opacity hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok disabled:cursor-wait disabled:opacity-50"
              >
                {busy === "enrollment" ? "创建中…" : "生成注册码"}
              </button>
            </div>
            {enrollment ? (
              <div className="mt-4 rounded border border-ok/40 bg-ok/5 p-3">
                <div className="flex items-center gap-2 text-sm text-ok">
                  <CheckCircle2 size={17} aria-hidden="true" />
                  有效至 {formatTime(enrollment.expires_at)}
                </div>
                <p className="mt-3 text-xs text-muted">在目标 VPS 以 root 执行；请先通过独立可信渠道核对 Panel URL、版本和发布公钥。</p>
                <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-all rounded bg-surface p-3 font-head text-xs leading-5 text-fg">{installCommand}</pre>
                <button
                  type="button"
                  onClick={() => void navigator.clipboard?.writeText(installCommand)}
                  className="mt-2 inline-flex min-h-11 cursor-pointer items-center gap-2 rounded border border-edge px-3 py-2 text-sm text-fg hover:border-ok focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok"
                >
                  <Copy size={16} aria-hidden="true" />复制安装命令
                </button>
              </div>
            ) : null}
          </section>

          <section className="rounded border border-edge bg-panel p-4" aria-labelledby="release-import-title">
            <div className="flex items-center gap-2">
              <ShieldCheck size={18} className="text-ok" aria-hidden="true" />
              <h2 id="release-import-title" className="font-head text-fg">导入签名发布</h2>
            </div>
            <p className="mt-1 text-sm text-muted">Panel 会验证 Ed25519 签名、有效期、平台和最低 Panel 版本。</p>
            <form className="mt-3 grid gap-3" onSubmit={onImportRelease}>
              <label className="text-sm text-muted">
                规范化 manifest JSON（必填）
                <textarea
                  required
                  rows={6}
                  value={manifestText}
                  onChange={(event) => onManifestTextChange?.(event.target.value)}
                  className="mt-1 w-full rounded border border-edge bg-surface px-3 py-2 font-head text-xs text-fg outline-none focus-visible:ring-2 focus-visible:ring-ok"
                />
              </label>
              <label className="text-sm text-muted">
                Ed25519 签名（Base64，必填）
                <textarea
                  required
                  rows={2}
                  value={signature}
                  onChange={(event) => onSignatureChange?.(event.target.value)}
                  className="mt-1 w-full rounded border border-edge bg-surface px-3 py-2 font-head text-xs text-fg outline-none focus-visible:ring-2 focus-visible:ring-ok"
                />
              </label>
              <button
                type="submit"
                disabled={busy === "release" || !manifestText.trim() || !signature.trim()}
                className="min-h-11 cursor-pointer justify-self-start rounded border border-ok px-4 py-2 text-sm text-ok transition-colors hover:bg-ok/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok disabled:cursor-not-allowed disabled:opacity-50"
              >
                {busy === "release" ? "验证中…" : "验证并导入"}
              </button>
            </form>
          </section>

          <section className="rounded border border-edge bg-panel p-4" aria-labelledby="fleet-upgrade-title">
            <div className="flex items-center gap-2">
              <UploadCloud size={18} className="text-warn" aria-hidden="true" />
              <h2 id="fleet-upgrade-title" className="font-head text-fg">批量升级</h2>
            </div>
            <p className="mt-1 text-sm text-muted">仅可选择平台匹配且声明升级能力的 Agent；所有命令要么一起写入，要么全部不写入。</p>
            {releases.length === 0 ? (
              <p className="mt-3 text-sm text-muted">请先导入一个有效的签名发布。</p>
            ) : (
              <form className="mt-3 space-y-4" onSubmit={onCreateUpgrades}>
                <label className="block text-sm text-muted">
                  签名发布
                  <select
                    value={effectiveReleaseID}
                    onChange={(event) => onSelectedReleaseIDChange?.(Number(event.target.value))}
                    className="mt-1 min-h-11 w-full rounded border border-edge bg-surface px-3 py-2 text-fg outline-none focus-visible:ring-2 focus-visible:ring-ok"
                  >
                    {releases.map((release) => (
                      <option key={release.id} value={release.id}>
                        {release.manifest.version} · {release.manifest.os}/{release.manifest.arch} · {formatBytes(release.manifest.byte_size)}
                      </option>
                    ))}
                  </select>
                </label>

                {selectedRelease ? (
                  <dl className="grid gap-2 rounded border border-edge bg-surface p-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
                    <ReleaseFact label="版本" value={selectedRelease.manifest.version} />
                    <ReleaseFact label="平台" value={`${selectedRelease.manifest.os}/${selectedRelease.manifest.arch}`} />
                    <ReleaseFact label="大小" value={formatBytes(selectedRelease.manifest.byte_size)} />
                    <ReleaseFact label="SHA-256" value={selectedRelease.manifest.sha256} mono />
                  </dl>
                ) : null}

                <fieldset>
                  <legend className="text-sm text-muted">升级目标（最多 100 个）</legend>
                  <div className="mt-2 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {agents.map((agent) => {
                      const compatible = isCompatible(agent, selectedRelease);
                      return (
                        <label
                          key={agent.agent_id}
                          className={`flex min-h-11 items-center gap-3 rounded border px-3 py-2 text-sm ${
                            compatible ? "cursor-pointer border-edge bg-surface text-fg" : "cursor-not-allowed border-edge/60 text-muted opacity-60"
                          }`}
                        >
                          <input
                            type="checkbox"
                            disabled={!compatible}
                            checked={selectedAgentIDs.includes(agent.agent_id)}
                            onChange={(event) => toggleAgent(agent.agent_id, event.target.checked)}
                            className="h-4 w-4 accent-green-500"
                          />
                          <span className="min-w-0">
                            <span className="block truncate">{agent.display_name}</span>
                            <span className="block text-xs text-muted">{agent.agent_version} · {agent.os}/{agent.arch}</span>
                          </span>
                        </label>
                      );
                    })}
                  </div>
                </fieldset>

                {!recentReauth ? (
                  <div className="rounded border border-warn/40 bg-warn/5 p-3">
                    <p className="text-sm text-warn">需要重新验证密码</p>
                    <p className="mt-1 text-xs text-muted">升级要求最近 10 分钟内重新认证。</p>
                    <div className="mt-2 flex flex-col gap-2 sm:flex-row" role="group" aria-label="重新验证密码">
                      <input
                        type="password"
                        required
                        autoComplete="current-password"
                        value={password}
                        onChange={(event) => onPasswordChange?.(event.target.value)}
                        aria-label="当前密码"
                        className="min-h-11 flex-1 rounded border border-edge bg-surface px-3 py-2 text-fg outline-none focus-visible:ring-2 focus-visible:ring-warn"
                      />
                      <button
                        type="button"
                        disabled={busy === "reauth" || !password}
                        onClick={onReauthenticate}
                        className="min-h-11 cursor-pointer rounded border border-warn px-4 py-2 text-sm text-warn hover:bg-warn/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-warn disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        {busy === "reauth" ? "验证中…" : "验证密码"}
                      </button>
                    </div>
                  </div>
                ) : null}

                <label className="block text-sm text-muted">
                  输入版本号“{selectedRelease?.manifest.version}”确认（必填）
                  <input
                    required
                    value={versionConfirmation}
                    onChange={(event) => onVersionConfirmationChange?.(event.target.value)}
                    className="mt-1 min-h-11 w-full rounded border border-danger/50 bg-surface px-3 py-2 font-head text-fg outline-none focus-visible:ring-2 focus-visible:ring-danger"
                  />
                </label>
                <button
                  type="submit"
                  data-testid="confirm-upgrade"
                  disabled={!readyToUpgrade || busy === "upgrade"}
                  className="min-h-11 cursor-pointer rounded bg-danger px-4 py-2 text-sm font-medium text-white transition-opacity hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {busy === "upgrade" ? "创建升级命令中…" : `确认升级 ${selectedAgentIDs.length} 个 Agent`}
                </button>
              </form>
            )}
          </section>
        </>
      ) : null}
    </div>
  );
}

function isRecentReauthentication(timestamp: number): boolean {
  const now = Math.floor(Date.now() / 1000);
  return timestamp > 0 && timestamp <= now && now - timestamp <= 10 * 60;
}

function isCompatible(agent: AgentIdentity, release?: Release): boolean {
  return Boolean(
    release &&
      agent.capabilities.includes("upgrade") &&
      agent.os === release.manifest.os &&
      agent.arch === release.manifest.arch,
  );
}

function capabilityLabel(value: AgentIdentity["capabilities"][number]): string {
  return {
    collect_now: "采集",
    reload_config: "重载配置",
    self_check: "自检",
    upgrade: "升级",
  }[value];
}

function buildInstallCommand(origin: string, version: string, enrollment: Enrollment): string {
  const releaseKey = enrollment.release_public_key_hex;
  const releaseTag = /^v\d+\.\d+\.\d+$/.test(version) ? version : "<release-tag>";
  return [
    `curl -fsSL https://raw.githubusercontent.com/s2005lg/net-probe/${releaseTag}/install.sh | env \\`,
    `  NET_PROBE_PANEL_URL=${shellQuote(origin)} \\`,
    `  NET_PROBE_VERSION=${shellQuote(version)} \\`,
    `  NET_PROBE_CA_FINGERPRINT=${shellQuote(enrollment.ca_fingerprint)} \\`,
    `  NET_PROBE_ENROLLMENT_CODE=${shellQuote(enrollment.code)} \\`,
    `  NET_PROBE_RELEASE_PUBLIC_KEY_HEX=${shellQuote(releaseKey)} \\`,
    "  bash",
  ].join("\n");
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

function ReleaseFact({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className={`mt-1 truncate text-fg ${mono ? "font-head text-xs" : ""}`} title={value}>{value}</dd>
    </div>
  );
}
