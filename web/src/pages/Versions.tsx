import { useEffect, useState } from "react";
import StatusBadge from "../components/StatusBadge";
import { api, nodeName, type Node, type Service, type VersionRow } from "../lib/api";
import { formatTime } from "../lib/format";
import { upgradeStatus, type UpgradeStatus } from "../lib/version";

type NodeServiceVersionRow = {
  node: Node;
  service: Service;
  latest: VersionRow | undefined;
  status: UpgradeStatus;
};

export default function VersionsPage() {
  const [rows, setRows] = useState<NodeServiceVersionRow[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;

    Promise.all([api.nodes().then((response) => response.items), api.versions()])
      .then(([nodes, versions]) => {
        if (cancelled) return;

        const versionByType = new Map(
          versions.map((version) => [version.service_type, version]),
        );
        const nextRows = nodes
          .flatMap((node) =>
            node.services
              .filter((service) => service.type !== "generic")
              .map((service) => {
              const latest = versionByType.get(service.type);
              return {
                node,
                service,
                latest,
                status: upgradeStatus(
                  service.version ?? "",
                  latest?.latest_version ?? "",
                ),
              };
            }),
          )
          .sort((a, b) => {
            const byNode = nodeName(a.node).localeCompare(nodeName(b.node));
            if (byNode !== 0) return byNode;

            const byType = a.service.type.localeCompare(b.service.type);
            if (byType !== 0) return byType;

            return (a.service.unit ?? "").localeCompare(b.service.unit ?? "");
          });

        setRows(nextRows);
        setError("");
      })
      .catch((e) => {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : String(e));
        }
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="space-y-4">
      {error && <p className="text-danger">{error}</p>}
      <div className="overflow-x-auto rounded border border-edge bg-panel">
        <table className="w-full text-left text-sm">
          <thead className="text-muted">
            <tr className="border-b border-edge">
              <th className="px-3 py-2 font-medium">节点</th>
              <th className="px-3 py-2 font-medium">服务</th>
              <th className="px-3 py-2 font-medium">当前版本</th>
              <th className="px-3 py-2 font-medium">最新版本</th>
              <th className="px-3 py-2 font-medium">状态</th>
              <th className="px-3 py-2 font-medium">更新时间</th>
              <th className="px-3 py-2 font-medium">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge">
            {rows.map((row) => (
              <tr
                key={`${row.node.node_id}-${row.service.type}-${row.service.unit ?? row.service.binary ?? row.service.main_pid ?? ""}`}
                className="hover:bg-surface"
              >
                <td className="px-3 py-2 text-fg">{nodeName(row.node)}</td>
                <td className="px-3 py-2 text-fg">
                  <div>{row.service.type}</div>
                  {row.service.unit ? (
                    <div className="text-xs text-muted">{row.service.unit}</div>
                  ) : null}
                </td>
                <td className="px-3 py-2 text-fg">{row.service.version || "—"}</td>
                <td className="px-3 py-2 text-fg">{row.latest?.latest_version || "—"}</td>
                <td className="px-3 py-2">
                  <StatusBadge status={row.status} />
                </td>
                <td className="px-3 py-2 text-muted">
                  {row.latest ? formatTime(row.latest.updated_at) : "—"}
                </td>
                <td className="px-3 py-2">
                  <button
                    type="button"
                    disabled
                    title="升级功能暂未开放"
                    className="rounded border border-edge px-2 py-1 text-xs text-muted opacity-40"
                  >
                    升级
                  </button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={7} className="px-3 py-6 text-center text-muted">
                  暂无节点服务版本数据
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
