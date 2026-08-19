const MAP: Record<string, { color: string; label: string }> = {
  online: { color: "var(--color-ok)", label: "在线" },
  offline: { color: "var(--color-danger)", label: "离线" },
  firing: { color: "var(--color-danger)", label: "告警中" },
  recovered: { color: "var(--color-warn)", label: "已恢复" },
  acknowledged: { color: "var(--color-muted)", label: "已确认" },
};

export default function StatusBadge({ status }: { status: string }) {
  const item = MAP[status] ?? { color: "var(--color-muted)", label: status };
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs"
      style={{ borderColor: item.color, color: item.color }}
    >
      <span
        className="h-1.5 w-1.5 rounded-full"
        style={{ background: item.color }}
      />
      {item.label}
    </span>
  );
}
