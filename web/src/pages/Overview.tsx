import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import {
  Bar,
  BarChart,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import StatusBadge from "../components/StatusBadge";
import { api, nodeName, type Node, type Overview } from "../lib/api";
import { formatBytes } from "../lib/format";

const TOP_N = 10;
const NODE_COLORS = [
  "#3b82f6",
  "#10b981",
  "#f59e0b",
  "#8b5cf6",
  "#ec4899",
  "#14b8a6",
  "#f97316",
  "#6366f1",
  "#84cc16",
  "#06b6d4",
];
const OTHER_COLOR = "#94a3b8";

type ChartDatum = {
  type: string;
  [key: string]: string | number;
};

export default function OverviewPage() {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [selectedType, setSelectedType] = useState<string | null>(null);
  const [hoveredNodeId, setHoveredNodeId] = useState<string | null>(null);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([api.overview(), api.nodes().then((response) => response.items)])
      .then(([o, n]) => {
        setOverview(o);
        setNodes(n);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  if (error) return <p className="text-danger">{error}</p>;
  if (!overview) return <p className="text-muted">加载中…</p>;

  const serviceTypes = Array.from(
    new Set(
      nodes
        .flatMap((n) => n.services.map((s) => s.type))
        .filter((type) => type && type !== "generic"),
    ),
  ).sort();

  const nodeAgg = nodes
    .map((node) => {
      const byType: Record<string, number> = {};
      for (const service of node.services) {
        if (!service.type) continue;
        byType[service.type] = (byType[service.type] ?? 0) + 1;
      }
      return {
        node,
        byType,
        total: Object.values(byType).reduce((sum, count) => sum + count, 0),
      };
    })
    .filter((item) => item.total > 0)
    .sort((a, b) => {
      if (a.total !== b.total) return b.total - a.total;
      if (a.node.last_report_at !== b.node.last_report_at) {
        return b.node.last_report_at - a.node.last_report_at;
      }
      return a.node.node_id.localeCompare(b.node.node_id);
    });

  const topNodes = nodeAgg.slice(0, TOP_N);
  const otherNodes = nodeAgg.slice(TOP_N);

  const chartData: ChartDatum[] = serviceTypes.map((type) => {
    const row: ChartDatum = { type };
    for (const item of topNodes) {
      row[item.node.node_id] = item.byType[type] ?? 0;
    }
    row.other = otherNodes.reduce((sum, item) => sum + (item.byType[type] ?? 0), 0);
    return row;
  });

  const detailRows = selectedType
    ? nodes
        .map((node) => ({
          node,
          count: node.services.filter((s) => s.type === selectedType && s.type !== "generic").length,
        }))
        .filter((row) => row.count > 0)
        .sort((a, b) => {
          if (a.count !== b.count) return b.count - a.count;
          if (a.node.last_report_at !== b.node.last_report_at) {
            return b.node.last_report_at - a.node.last_report_at;
          }
          return a.node.node_id.localeCompare(b.node.node_id);
        })
    : [];

  const focusNodeId = hoveredNodeId ?? selectedNodeId;
  const selectedNode = selectedNodeId
    ? nodes.find((node) => node.node_id === selectedNodeId) ?? null
    : null;

  const legendNodeId = (entry: {
    value?: unknown;
    dataKey?: unknown;
  }): string | null => {
    const dataKey = typeof entry.dataKey === "string" ? entry.dataKey : "";
    if (dataKey && nodeAgg.some((item) => item.node.node_id === dataKey)) {
      return dataKey;
    }
    const value =
      typeof entry.value === "string" || typeof entry.value === "number"
        ? String(entry.value)
        : "";
    const match = nodeAgg.find((item) => nodeName(item.node) === value);
    return match?.node.node_id ?? null;
  };

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
        {serviceTypes.length === 0 ? (
          <p className="text-muted">暂无数据</p>
        ) : (
          <>
            <ResponsiveContainer width="100%" height={280}>
              <BarChart data={chartData} onMouseLeave={() => setHoveredNodeId(null)}>
                <XAxis dataKey="type" stroke="var(--color-muted)" />
                <YAxis allowDecimals={false} stroke="var(--color-muted)" />
                <Tooltip
                  cursor={{ fill: "var(--color-edge)" }}
                  formatter={(value, name) => [value, name]}
                />
                <Legend
                  formatter={(value) => {
                    const node = nodeAgg.find((item) => item.node.node_id === value);
                    return node ? nodeName(node.node) : value;
                  }}
                  onMouseEnter={(data) => {
                    setHoveredNodeId(legendNodeId(data));
                  }}
                  onMouseLeave={() => setHoveredNodeId(null)}
                  onClick={(data) => {
                    setSelectedType(null);
                    const key = legendNodeId(data);
                    if (!key) return;
                    setSelectedNodeId((prev) => (prev === key ? null : key));
                  }}
                />
                {topNodes.map((item, index) => (
                  <Bar
                    key={item.node.node_id}
                    dataKey={item.node.node_id}
                    stackId="nodes"
                    name={nodeName(item.node)}
                    fill={NODE_COLORS[index % NODE_COLORS.length]}
                    fillOpacity={focusNodeId && focusNodeId !== item.node.node_id ? 0.25 : 1}
                    radius={[0, 0, 0, 0]}
                    onMouseEnter={() => setHoveredNodeId(item.node.node_id)}
                    onClick={() => {
                      setSelectedType(null);
                      setSelectedNodeId((prev) =>
                        prev === item.node.node_id ? null : item.node.node_id,
                      );
                    }}
                  />
                ))}
                {otherNodes.length > 0 ? (
                  <Bar
                    dataKey="other"
                    stackId="nodes"
                    name="其他"
                    fill={OTHER_COLOR}
                    fillOpacity={focusNodeId ? 0.25 : 1}
                    radius={[0, 0, 0, 0]}
                    onClick={(entry) => {
                      setSelectedNodeId(null);
                      setSelectedType(entry.type);
                    }}
                  />
                ) : null}
              </BarChart>
            </ResponsiveContainer>
            <p className="mt-2 text-xs text-muted">
              点击具体节点色块可查看该节点明细；点击“其他”可查看该服务类型的全部节点明细。
            </p>
          </>
        )}
      </section>

      {selectedType ? (
        <section className="rounded border border-edge bg-panel p-4">
          <div className="mb-3 flex items-center justify-between">
            <h2 className="font-head text-fg">{selectedType} 节点明细</h2>
            <button
              type="button"
              onClick={() => setSelectedType(null)}
              className="rounded border border-edge px-2 py-1 text-sm text-muted transition-colors hover:border-ok hover:text-fg"
            >
              关闭
            </button>
          </div>
          <div className="max-h-80 overflow-auto">
            <table className="w-full text-left text-sm">
              <thead className="text-muted">
                <tr>
                  <th className="px-3 py-2">节点</th>
                  <th className="px-3 py-2">数量</th>
                  <th className="px-3 py-2">状态</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge">
                {detailRows.map(({ node, count }) => (
                  <tr key={node.node_id} className="hover:bg-surface">
                    <td className="px-3 py-2 text-fg">
                      <Link
                        to={`/nodes/${encodeURIComponent(node.node_id)}`}
                        className="hover:text-ok"
                      >
                        {nodeName(node)}
                      </Link>
                    </td>
                    <td className="px-3 py-2 text-fg">{count}</td>
                    <td className="px-3 py-2">
                      <StatusBadge status={node.status} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      {selectedNode ? (
        <section className="rounded border border-edge bg-panel p-4">
          <div className="mb-3 flex items-center justify-between">
            <div className="flex items-center gap-3">
              <h2 className="font-head text-fg">
                <Link
                  to={`/nodes/${encodeURIComponent(selectedNode.node_id)}`}
                  className="hover:text-ok"
                >
                  {nodeName(selectedNode)}
                </Link>{" "}
                节点明细
              </h2>
              <StatusBadge status={selectedNode.status} />
            </div>
            <button
              type="button"
              onClick={() => setSelectedNodeId(null)}
              className="rounded border border-edge px-2 py-1 text-sm text-muted transition-colors hover:border-ok hover:text-fg"
            >
              关闭
            </button>
          </div>
          <div className="max-h-80 overflow-auto">
            <table className="w-full text-left text-sm">
              <thead className="text-muted">
                <tr>
                  <th className="px-3 py-2">服务类型</th>
                  <th className="px-3 py-2">版本</th>
                  <th className="px-3 py-2">状态</th>
                  <th className="px-3 py-2">累计上行流量</th>
                  <th className="px-3 py-2">累计下行流量</th>
                  <th className="px-3 py-2">在线连接数</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge">
                {selectedNode.services.map((service) => (
                  <tr key={`${service.type}-${service.unit || service.binary || service.main_pid || ""}`} className="hover:bg-surface">
                    <td className="px-3 py-2 text-fg">{service.type}</td>
                    <td className="px-3 py-2 text-fg">{service.version || "—"}</td>
                    <td className="px-3 py-2 text-fg">{service.active ? "运行中" : "已停止"}</td>
                    <td className="px-3 py-2 text-fg">{service.stats ? formatBytes(service.stats.tx) : "—"}</td>
                    <td className="px-3 py-2 text-fg">{service.stats ? formatBytes(service.stats.rx) : "—"}</td>
                    <td className="px-3 py-2 text-fg">{service.stats?.online_clients ?? "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      <section className="rounded border border-edge bg-panel p-4">
        <h2 className="mb-3 font-head text-fg">全部节点</h2>
        {nodes.length === 0 ? (
          <p className="text-muted">暂无节点</p>
        ) : (
          <ul className="divide-y divide-edge">
            {nodes.map((n) => (
              <li key={n.node_id}>
                <Link
                  to={`/nodes/${n.node_id}`}
                  className="flex items-center justify-between px-2 py-2 transition-colors hover:bg-surface focus:outline-none"
                >
                  <span className="text-fg">{nodeName(n)}</span>
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
