import { useEffect, useState } from "react";
import { api, type SettingsData } from "../lib/api";

export default function SettingsPage() {
  const [data, setData] = useState<SettingsData | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.settings()
      .then(setData)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  if (error) return <p className="text-danger">{error}</p>;
  if (!data) return <p className="text-muted">加载中…</p>;

  const groups: { title: string; rows: [string, string][] }[] = [
    {
      title: "服务",
      rows: [
        ["监听地址", data.listen_addr],
        ["数据目录", data.data_dir],
      ],
    },
    {
      title: "告警",
      rows: [["节点超时", data.node_timeout]],
    },
    {
      title: "数据保留",
      rows: [
        ["原始", `${data.retention.raw_days} 天`],
        ["小时", `${data.retention.hourly_days} 天`],
        ["天", `${data.retention.daily_days} 天`],
      ],
    },
    {
      title: "管理",
      rows: [["管理员", data.admin.user]],
    },
  ];

  return (
    <div className="grid gap-4 md:grid-cols-2">
      {groups.map((g) => (
        <section key={g.title} className="rounded border border-edge bg-panel p-4">
          <h2 className="mb-3 font-head text-fg">{g.title}</h2>
          <dl className="space-y-2 text-sm">
            {g.rows.map(([label, value]) => (
              <div
                key={label}
                className="flex justify-between border-b border-edge pb-2 last:border-0 last:pb-0"
              >
                <dt className="text-muted">{label}</dt>
                <dd className="text-fg">{value}</dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
    </div>
  );
}
