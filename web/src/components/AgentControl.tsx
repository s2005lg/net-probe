import { Activity, RefreshCw, ShieldAlert, Stethoscope } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import {
  api,
  type AgentAction,
  type AgentCommand,
  type AgentIdentity,
  type SessionUser,
} from "../lib/api";
import { formatRelative, formatTime } from "../lib/format";

const ACTION_LABELS: Record<AgentAction, string> = {
  collect_now: "立即采集",
  reload_config: "重新加载配置",
  self_check: "运行自检",
  upgrade: "升级 Agent",
};

const STATE_LABELS: Record<AgentCommand["state"], string> = {
  queued: "已排队",
  dispatched: "已发送",
  accepted: "已接收",
  running: "执行中",
  succeeded: "成功",
  failed: "失败",
  expired: "已过期",
};

const RESULT_LABELS: Record<string, string> = {
  accepted: "已接收",
  running: "执行中",
  report_acknowledged: "采集并上报成功",
  report_not_acknowledged: "Panel 未确认上报",
  reload_applied: "配置已重新加载",
  reload_failed: "配置重新加载失败",
  self_check_completed: "自检完成",
  self_check_failed: "自检未通过",
  self_check_unavailable: "自检不可用",
  upgrade_staged: "升级已交给系统助手",
  upgrade_applied: "升级完成",
  upgrade_rolled_back: "升级失败，已回滚",
  upgrade_rollback_failed: "升级与回滚均失败",
  upgrade_not_applied: "升级未应用",
  upgrade_rejected: "系统助手拒绝升级",
  upgrade_disabled: "此 Agent 未启用升级",
  upgrade_busy: "已有升级正在执行",
  invalid_payload: "命令参数无效",
  unsupported_action: "Agent 不支持此操作",
  expired: "命令已过期",
  state_write_failed: "Agent 状态写入失败",
  handler_failed: "Agent 执行失败",
  invalid_handler_result: "Agent 返回了无效结果",
  invalid_release: "发布签名或兼容性无效",
  download_failed: "发布文件下载失败",
  artifact_verification_failed: "发布文件校验失败",
  stage_write_failed: "升级暂存失败",
  helper_result_invalid: "系统助手结果无效",
  pending_request_invalid: "升级请求损坏",
  panel_version_mismatch: "Panel 版本与签名命令不一致",
  invalid_command_envelope: "升级命令签名封装无效",
};

const TERMINAL = new Set<AgentCommand["state"]>(["succeeded", "failed", "expired"]);

export interface AgentControlProps {
  agent: AgentIdentity;
  actor: SessionUser;
  initialCommands?: AgentCommand[];
  onRevoked?: () => void;
}

export default function AgentControl({
  agent,
  actor,
  initialCommands = [],
  onRevoked,
}: AgentControlProps) {
  const [commands, setCommands] = useState(initialCommands);
  const [runningAction, setRunningAction] = useState<AgentAction | "revoke" | "">("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const loadHistory = useCallback(async () => {
    const response = await api.commandHistory(agent.agent_id);
    setCommands(response.items);
    return response.items;
  }, [agent.agent_id]);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    async function poll() {
      try {
        const items = await api.commandHistory(agent.agent_id);
        if (cancelled) return;
        setCommands(items.items);
        setError("");
        const hasActive = items.items.some((item) => !TERMINAL.has(item.state));
        timer = setTimeout(poll, hasActive ? 10_000 : 30_000);
      } catch (cause) {
        if (cancelled) return;
        setError(cause instanceof Error ? cause.message : String(cause));
        timer = setTimeout(poll, 30_000);
      }
    }

    void poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [agent.agent_id]);

  async function execute(action: Exclude<AgentAction, "upgrade">) {
    setRunningAction(action);
    setError("");
    setNotice("");
    const payload =
      action === "self_check"
        ? { checks: ["config", "certificate", "panel", "detectors", "filesystem", "update"] }
        : {};
    try {
      await api.createCommand(agent.agent_id, action, payload);
      await loadHistory();
      setNotice(`${ACTION_LABELS[action]}命令已创建`);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setRunningAction("");
    }
  }

  async function revoke() {
    const typed = window.prompt(`输入“${agent.display_name}”以确认吊销此 Agent 证书。节点遥测会保留。`);
    if (typed !== agent.display_name) return;
    setRunningAction("revoke");
    setError("");
    try {
      await api.revokeAgent(agent.agent_id);
      setNotice("Agent 证书已吊销");
      onRevoked?.();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setRunningAction("");
    }
  }

  const canOperate = actor.role === "operator" || actor.role === "admin";
  const has = (action: AgentAction) => agent.capabilities.includes(action);

  return (
    <section className="rounded border border-edge bg-panel p-4" aria-labelledby="agent-control-title">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 id="agent-control-title" className="font-head text-fg">Agent 控制</h2>
          <p className="mt-1 text-sm text-muted">
            控制通道 {agent.control_status === "online" ? "在线" : "离线"}
            {agent.last_heartbeat_at > 0 ? ` · 最后心跳 ${formatRelative(agent.last_heartbeat_at)}` : " · 尚无心跳"}
          </p>
        </div>
        <span
          className={`rounded-full border px-2.5 py-1 text-xs ${
            agent.control_status === "online"
              ? "border-ok/40 bg-ok/10 text-ok"
              : "border-edge bg-surface text-muted"
          }`}
        >
          {agent.control_status === "online" ? "控制在线" : "控制离线"}
        </span>
      </div>

      {canOperate ? (
        <div className="mt-4 flex flex-wrap gap-2" aria-label="Agent 操作">
          {has("collect_now") ? (
            <ActionButton icon={Activity} label="立即采集" busy={runningAction === "collect_now"} disabled={Boolean(runningAction)} onClick={() => void execute("collect_now")} />
          ) : null}
          {has("reload_config") ? (
            <ActionButton icon={RefreshCw} label="重新加载配置" busy={runningAction === "reload_config"} disabled={Boolean(runningAction)} onClick={() => void execute("reload_config")} />
          ) : null}
          {has("self_check") ? (
            <ActionButton icon={Stethoscope} label="运行自检" busy={runningAction === "self_check"} disabled={Boolean(runningAction)} onClick={() => void execute("self_check")} />
          ) : null}
          {actor.role === "admin" ? (
            <button
              type="button"
              disabled={Boolean(runningAction)}
              onClick={() => void revoke()}
              className="inline-flex min-h-11 cursor-pointer items-center gap-2 rounded border border-danger/50 px-3 py-2 text-sm text-danger transition-colors hover:bg-danger/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger disabled:cursor-wait disabled:opacity-50"
            >
              <ShieldAlert size={17} aria-hidden="true" />
              {runningAction === "revoke" ? "吊销中…" : "吊销证书"}
            </button>
          ) : null}
        </div>
      ) : null}

      <div className="mt-3 min-h-5 text-sm" aria-live="polite">
        {error ? <span className="text-danger">{error}</span> : null}
        {!error && notice ? <span className="text-ok">{notice}</span> : null}
      </div>

      <div className="mt-2">
        <h3 className="font-head text-sm text-fg">命令记录</h3>
        {commands.length === 0 ? (
          <p className="mt-2 text-sm text-muted">暂无命令</p>
        ) : (
          <div className="mt-2 overflow-x-auto rounded border border-edge">
            <table className="w-full min-w-[760px] text-left text-sm">
              <thead className="bg-surface text-muted">
                <tr>
                  <th className="px-3 py-2 font-medium">操作</th>
                  <th className="px-3 py-2 font-medium">状态</th>
                  <th className="px-3 py-2 font-medium">时间</th>
                  <th className="px-3 py-2 font-medium">耗时</th>
                  <th className="px-3 py-2 font-medium">结果</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge">
                {commands.map((item) => (
                  <CommandRow
                    key={`${item.command.command_id}-${item.state}-${item.finished_at}`}
                    item={item}
                    offline={agent.control_status === "offline"}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}

function ActionButton({
  icon: Icon,
  label,
  busy,
  disabled,
  onClick,
}: {
  icon: typeof Activity;
  label: string;
  busy: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className="inline-flex min-h-11 cursor-pointer items-center gap-2 rounded border border-edge px-3 py-2 text-sm text-fg transition-colors hover:border-ok hover:text-ok focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ok disabled:cursor-wait disabled:opacity-50"
    >
      <Icon size={17} aria-hidden="true" />
      {busy ? "提交中…" : label}
    </button>
  );
}

function CommandRow({ item, offline }: { item: AgentCommand; offline: boolean }) {
  const queuedOffline = offline && (item.state === "queued" || item.state === "dispatched");
  const duration = commandDuration(item);
  return (
    <tr>
      <td className="px-3 py-2 text-fg">{ACTION_LABELS[item.command.action]}</td>
      <td className="px-3 py-2">
        <span className={item.state === "failed" ? "text-danger" : item.state === "succeeded" ? "text-ok" : "text-muted"}>
          {queuedOffline ? "离线等待" : STATE_LABELS[item.state]}
        </span>
        {(item.state === "queued" || item.state === "dispatched") ? (
          <div className="mt-0.5 text-xs text-muted">过期于 {formatTime(item.command.expires_at)}</div>
        ) : null}
      </td>
      <td className="px-3 py-2 text-muted">{formatTime(item.command.issued_at)}</td>
      <td className="px-3 py-2 text-muted">{duration}</td>
      <td className="px-3 py-2 text-muted">{resultSummary(item)}</td>
    </tr>
  );
}

function commandDuration(item: AgentCommand): string {
  const start = item.started_at || item.accepted_at || item.dispatched_at;
  if (!start) return "—";
  const end = item.finished_at || Math.floor(Date.now() / 1000);
  const seconds = Math.max(0, end - start);
  return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
}

function resultSummary(item: AgentCommand): string {
  if (!item.result_code) return "—";
  const base = RESULT_LABELS[item.result_code] ?? "未识别结果";
  const version = typeof item.result?.version === "string" && /^v\d+\.\d+\.\d+$/.test(item.result.version)
    ? item.result.version
    : "";
  return version ? `${base} · ${version}` : base;
}
