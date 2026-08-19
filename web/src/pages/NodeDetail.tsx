import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import StatusBadge from "../components/StatusBadge";
import { api, type Metric, type Node } from "../lib/api";
import {
  formatBytes,
  formatClock,
  formatDate,
  formatRelative,
  formatTime,
  formatUptime,
} from "../lib/format";

export default function NodeDetailPage() {
  const { id = "" } = useParams();
  const [node, setNode] = useState<Node | null>(null);
  const [metrics, setMetrics] = useState<Metric[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!id) return;
    Promise.all([api.node(id), api.nodeMetrics(id)])
      .then(([n, m]) => {
        setNode(n);
        setMetrics(m);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, [id]);

  if (error) return <p className="text-danger">{error}</p>;
  if (!node) return <p className="text-muted">加载中…</p>;

  const { host } = node;
  const rangeLabel =
    metrics.length > 0
      ? `${formatDate(metrics[0].ts)} ~ ${formatDate(metrics[metrics.length - 1].ts)}`
      : "";

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="font-head text-2xl text-fg">{node.alias || node.node_id}</h1>
          <p className="text-sm text-muted">
            {node.node_id} · 最后上报 {formatRelative(node.last_report_at)}
          </p>
        </div>
        <StatusBadge status={node.status} />
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <InfoCard label="主机名" value={host.hostname} />
        <InfoCard label="系统" value={[host.os, host.kernel, host.arch].filter(Boolean).join(" ")} />
        <InfoCard label="IPv4" value={host.ipv4 || host.ipv6 || "—"} />
        <InfoCard label="运行时长" value={formatUptime(host.uptime_seconds)} />
      </div>

      <section className="rounded border border-edge bg-panel p-4">
        <h2 className="mb-3 font-head text-fg">服务</h2>
        {node.services.length === 0 ? (
          <p className="text-muted">暂无服务</p>
        ) : (
          <div className="grid gap-3 md:grid-cols-2">
            {node.services.map((s, i) => (
              <div key={`${s.type}-${i}`} className="rounded border border-edge bg-surface p-3">
                <div className="flex items-center justify-between">
                  <span className="font-medium text-fg">{s.type}</span>
                  <StatusBadge status={s.active ? "online" : "offline"} />
                </div>
                <dl className="mt-2 space-y-1 text-sm text-muted">
                  <Row label="版本" value={s.version || "—"} />
                  <Row label="端口" value={s.listen.map((l) => l.port).join(", ") || "—"} />
                  <Row label="证书" value={s.cert ? `${s.cert.days_left} 天` : "—"} />
                  <Row
                    label="流量"
                    value={s.stats ? `↓${formatBytes(s.stats.rx)} ↑${formatBytes(s.stats.tx)}` : "—"}
                  />
                  <Row label="在线连接" value={s.stats?.online_clients?.toString() ?? "—"} />
                </dl>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="rounded border border-edge bg-panel p-4">
        <h2 className="font-head text-fg">资源趋势</h2>
        {rangeLabel && <p className="mt-1 mb-3 text-xs text-muted">{rangeLabel}</p>}
        {metrics.length === 0 ? (
          <p className="text-muted">暂无历史数据</p>
        ) : (
          <div className="space-y-6">
            <div>
              <h3 className="mb-2 text-sm text-muted">负载</h3>
              <ResponsiveContainer width="100%" height={220}>
                <LineChart data={metrics}>
                  <CartesianGrid stroke="var(--color-edge)" strokeDasharray="3 3" />
                  <XAxis
                    dataKey="ts"
                    tickFormatter={(v) => formatClock(Number(v))}
                    minTickGap={24}
                    stroke="var(--color-muted)"
                  />
                  <YAxis stroke="var(--color-muted)" domain={[0, "auto"]} />
                  <Tooltip labelFormatter={(v) => formatTime(Number(v))} />
                  <Legend />
                  <Line type="monotone" dataKey="load1" name="1分钟负载" stroke="var(--color-ok)" dot={false} />
                  <Line type="monotone" dataKey="load5" name="5分钟负载" stroke="var(--color-warn)" dot={false} />
                  <Line type="monotone" dataKey="load15" name="15分钟负载" stroke="var(--color-danger)" dot={false} />
                </LineChart>
              </ResponsiveContainer>
            </div>
            <div>
              <h3 className="mb-2 text-sm text-muted">内存 / 磁盘</h3>
              <ResponsiveContainer width="100%" height={220}>
                <LineChart data={metrics}>
                  <CartesianGrid stroke="var(--color-edge)" strokeDasharray="3 3" />
                  <XAxis
                    dataKey="ts"
                    tickFormatter={(v) => formatClock(Number(v))}
                    minTickGap={24}
                    stroke="var(--color-muted)"
                  />
                  <YAxis
                    stroke="var(--color-muted)"
                    domain={[0, 100]}
                    tickFormatter={(v) => `${v}%`}
                  />
                  <Tooltip
                    labelFormatter={(v) => formatTime(Number(v))}
                    formatter={(value, name) => [`${Number(value).toFixed(1)}%`, name]}
                  />
                  <Legend />
                  <Line type="monotone" dataKey="mem_used_pct" name="内存使用率" stroke="var(--color-warn)" dot={false} />
                  <Line type="monotone" dataKey="disk_used_pct" name="磁盘使用率" stroke="var(--color-danger)" dot={false} />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </div>
        )}
      </section>
    </div>
  );
}

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border border-edge bg-panel p-3">
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-1 truncate text-sm text-fg">{value || "—"}</div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between">
      <dt>{label}</dt>
      <dd className="text-fg">{value}</dd>
    </div>
  );
}
