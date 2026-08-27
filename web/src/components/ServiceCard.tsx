import type { Service } from "../lib/api";
import { formatBytes } from "../lib/format";
import {
  metricLabel,
  normalizeOnlineClients,
  normalizeTraffic,
  protocolLabels,
  type NormalizedCount,
  type NormalizedTraffic,
} from "../lib/serviceTelemetry";
import StatusBadge from "./StatusBadge";

export default function ServiceCard({ service }: { service: Service }) {
  const traffic = normalizeTraffic(service);
  const onlineClients = normalizeOnlineClients(service);
  const protocols = protocolLabels(service);

  return (
    <div className="rounded border border-edge bg-surface p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <span className="font-medium text-fg">{service.type}</span>
          {protocols.map((protocol) => (
            <span
              key={protocol}
              className="rounded border border-edge px-1.5 py-0.5 text-xs text-muted"
            >
              {protocol}
            </span>
          ))}
        </div>
        <StatusBadge status={service.status || (service.active ? "online" : "offline")} />
      </div>
      <dl className="mt-2 space-y-1 text-sm text-muted">
        <Row label="版本" value={service.version || "—"} />
        <Row label="端口" value={service.listen.map((listen) => listen.port).join(", ") || "—"} />
        <Row label="证书" value={service.cert ? `${service.cert.days_left} 天` : "—"} />
        <MetricRow label="流量" metric={traffic} value={trafficValue(traffic)} />
        <MetricRow label="在线连接" metric={onlineClients} value={onlineClientsValue(onlineClients)} />
      </dl>
    </div>
  );
}

function trafficValue(traffic: NormalizedTraffic): string {
  return traffic.kind === "ok"
    ? `↓${formatBytes(traffic.rx)} ↑${formatBytes(traffic.tx)}`
    : metricLabel(traffic);
}

function onlineClientsValue(onlineClients: NormalizedCount): string {
  return onlineClients.kind === "ok" ? String(onlineClients.value) : metricLabel(onlineClients);
}

function MetricRow({
  label,
  metric,
  value,
}: {
  label: string;
  metric: NormalizedTraffic | NormalizedCount;
  value: string;
}) {
  return <Row label={label} value={value} title={metric.kind === "error" ? metric.errorCode : undefined} />;
}

function Row({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="flex justify-between gap-3">
      <dt>{label}</dt>
      <dd className="text-right text-fg" title={title}>{value}</dd>
    </div>
  );
}
