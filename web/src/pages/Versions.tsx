import { useEffect, useState } from "react";
import { api, type VersionRow } from "../lib/api";
import { formatTime } from "../lib/format";

export default function VersionsPage() {
  const [versions, setVersions] = useState<VersionRow[]>([]);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [value, setValue] = useState("");

  async function load() {
    try {
      setVersions(await api.versions());
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function save(serviceType: string) {
    if (!value.trim()) return;
    try {
      await api.patchVersion(serviceType, value.trim());
      setEditing(null);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="space-y-4">
      {error && <p className="text-danger">{error}</p>}
      <div className="overflow-x-auto rounded border border-edge bg-panel">
        <table className="w-full text-left text-sm">
          <thead className="text-muted">
            <tr className="border-b border-edge">
              <th className="px-3 py-2 font-medium">服务</th>
              <th className="px-3 py-2 font-medium">最新版本</th>
              <th className="px-3 py-2 font-medium">来源</th>
              <th className="px-3 py-2 font-medium">更新时间</th>
              <th className="px-3 py-2 font-medium">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge">
            {versions.map((v) => (
              <tr key={v.service_type} className="hover:bg-surface">
                <td className="px-3 py-2 text-fg">{v.service_type}</td>
                <td className="px-3 py-2 text-fg">
                  {editing === v.service_type ? (
                    <input
                      value={value}
                      onChange={(e) => setValue(e.target.value)}
                      autoFocus
                      aria-label="版本号"
                      className="w-28 rounded border border-edge bg-surface px-2 py-1 text-fg outline-none focus:border-ok"
                    />
                  ) : (
                    v.latest_version
                  )}
                </td>
                <td className="px-3 py-2 text-muted">{v.source}</td>
                <td className="px-3 py-2 text-muted">{formatTime(v.updated_at)}</td>
                <td className="px-3 py-2">
                  {editing === v.service_type ? (
                    <div className="flex gap-2">
                      <button
                        onClick={() => save(v.service_type)}
                        className="rounded bg-ok px-2 py-1 text-xs text-surface transition-opacity hover:opacity-90 focus:outline-none"
                      >
                        保存
                      </button>
                      <button
                        onClick={() => setEditing(null)}
                        className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:text-fg focus:outline-none"
                      >
                        取消
                      </button>
                    </div>
                  ) : (
                    <button
                      onClick={() => {
                        setEditing(v.service_type);
                        setValue(v.latest_version);
                      }}
                      className="rounded border border-edge px-2 py-1 text-xs text-muted transition-colors hover:border-ok hover:text-ok focus:outline-none"
                    >
                      覆盖
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {versions.length === 0 && (
              <tr>
                <td colSpan={5} className="px-3 py-6 text-center text-muted">
                  暂无版本数据
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
