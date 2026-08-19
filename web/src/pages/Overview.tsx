import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import {
  Bar,
  BarChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import StatusBadge from "../components/StatusBadge";
import { api, type Node, type Overview } from "../lib/api";

export default function OverviewPage() {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([api.overview(), api.nodes()])
      .then(([o, n]) => {
        setOverview(o);
        setNodes(n);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  if (error) return <p className="text-danger">{error}</p>;
  if (!overview) return <p className="text-muted">加载中…</p>;

  const kpis = [
    { label: "节点总数", value: overview.nodes_total, to: "/nodes" },
    { label: "在线", value: overview.nodes_online, to: "/nodes?status=online" },
    { label: "活跃告警", value: overview.alerts_active, to: "/alerts" },
    { label: "服务实例", value: overview.services_total, to: "/nodes" },
  ];

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {kpis.map((k) => (
          <Link
            key={k.label}
            to={k.to}
            className="rounded border border-edge bg-panel p-4 transition-colors hover:border-ok focus:outline-none"
          >
            <div className="text-sm text-muted">{k.label}</div>
            <div className="mt-1 font-head text-2xl text-fg">{k.value}</div>
          </Link>
        ))}
      </div>

      <section className="rounded border border-edge bg-panel p-4">
        <h2 className="mb-3 font-head text-fg">服务类型分布</h2>
        {overview.service_distribution.length === 0 ? (
          <p className="text-muted">暂无数据</p>
        ) : (
          <ResponsiveContainer width="100%" height={240}>
            <BarChart data={overview.service_distribution}>
              <XAxis dataKey="type" stroke="var(--color-muted)" />
              <YAxis allowDecimals={false} stroke="var(--color-muted)" />
              <Tooltip cursor={{ fill: "var(--color-edge)" }} />
              <Bar dataKey="count" fill="var(--color-ok)" radius={[3, 3, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        )}
      </section>

      <section className="rounded border border-edge bg-panel p-4">
        <h2 className="mb-3 font-head text-fg">最近节点</h2>
        {nodes.length === 0 ? (
          <p className="text-muted">暂无节点</p>
        ) : (
          <ul className="divide-y divide-edge">
            {nodes.slice(0, 10).map((n) => (
              <li key={n.node_id}>
                <Link
                  to={`/nodes/${n.node_id}`}
                  className="flex items-center justify-between px-2 py-2 transition-colors hover:bg-surface focus:outline-none"
                >
                  <span className="text-fg">{n.alias || n.node_id}</span>
                  <StatusBadge status={n.status} />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
