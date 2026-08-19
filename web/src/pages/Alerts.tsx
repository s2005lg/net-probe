import { useEffect, useState } from "react";
import StatusBadge from "../components/StatusBadge";
import { api, type Alert } from "../lib/api";
import { formatTime } from "../lib/format";

export default function AlertsPage() {
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  async function load(next = status) {
    setLoading(true);
    try {
      setAlerts(await api.alerts(next || undefined));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load("");
  }, []);

  async function ack(id: number) {
    try {
      await api.ackAlert(id);
      await load(status);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <select
          value={status}
          onChange={(e) => {
            setStatus(e.target.value);
            load(e.target.value);
          }}
          aria-label="状态过滤"
          className="rounded border border-edge bg-panel px-3 py-2 text-sm text-fg outline-none focus:border-ok"
        >
          <option value="">全部状态</option>
          <option value="firing">告警中</option>
          <option value="recovered">已恢复</option>
          <option value="acknowledged">已确认</option>
        </select>
      </div>

      {error && <p className="text-danger">{error}</p>}

      <div className="overflow-x-auto rounded border border-edge bg-panel">
        <table className="w-full text-left text-sm">
          <thead className="text-muted">
            <tr className="border-b border-edge">
              <th className="px-3 py-2 font-medium">节点</th>
              <th className="px-3 py-2 font-medium">规则</th>
              <th className="px-3 py-2 font-medium">状态</th>
              <th className="px-3 py-2 font-medium">信息</th>
              <th className="px-3 py-2 font-medium">首次</th>
              <th className="px-3 py-2 font-medium">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge">
            {alerts.map((a) => (
              <tr key={a.id} className="hover:bg-surface">
                <td className="px-3 py-2 text-fg">{a.hostname || a.node_id}</td>
                <td className="px-3 py-2 text-muted">{a.rule}</td>
                <td className="px-3 py-2">
                  <StatusBadge status={a.status} />
                </td>
                <td className="px-3 py-2 text-muted">{a.message}</td>
                <td className="px-3 py-2 text-muted">{formatTime(a.first_seen_at)}</td>
                <td className="px-3 py-2">
                  {a.status !== "acknowledged" && (
                    <button
                      onClick={() => ack(a.id)}
                      className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:border-ok hover:text-ok focus:outline-none"
                    >
                      确认
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {!loading && alerts.length === 0 && (
              <tr>
                <td colSpan={6} className="px-3 py-6 text-center text-muted">
                  暂无告警
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
