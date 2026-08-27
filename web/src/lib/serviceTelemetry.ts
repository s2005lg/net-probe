import type {
  CapabilityReasonCode,
  MetricCapability,
  Service,
  TelemetryErrorCode,
} from "./api";

type MetricKind = "unsupported" | "not_implemented" | "not_configured" | "disabled" | "error" | "unknown";

export type NormalizedTraffic =
  | { kind: "ok"; tx: number; rx: number; legacy: boolean }
  | { kind: MetricKind; errorCode?: string };

export type NormalizedCount =
  | { kind: "ok"; value: number; legacy: boolean }
  | { kind: MetricKind; errorCode?: string };

const TELEMETRY_ERROR_CODES = new Set<TelemetryErrorCode>([
  "timeout",
  "unauthorized",
  "connection_failed",
  "invalid_response",
  "command_failed",
  "config_unreadable",
  "unknown",
]);

function capabilityKind(capability?: MetricCapability): MetricKind {
  if (capability?.support !== "unsupported") return "unknown";

  switch (capability.reason_code as CapabilityReasonCode | undefined) {
    case "native_api_unavailable":
      return "unsupported";
    case "collector_not_implemented":
      return "not_implemented";
    default:
      return "unknown";
  }
}

function telemetryKind(state: unknown, errorCode?: string): { kind: MetricKind; errorCode?: string } {
  switch (state) {
    case "not_configured":
    case "disabled":
      return { kind: state };
    case "error":
      return TELEMETRY_ERROR_CODES.has(errorCode as TelemetryErrorCode)
        ? { kind: "error", errorCode }
        : { kind: "unknown" };
    default:
      return { kind: "unknown" };
  }
}

function isNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

export function normalizeTraffic(service: Service): NormalizedTraffic {
  const telemetry = service.telemetry;
  if (telemetry) {
    const traffic = telemetry.traffic;
    if (traffic?.state === "ok") {
      return isNumber(traffic.tx_bytes) && isNumber(traffic.rx_bytes)
        ? { kind: "ok", tx: traffic.tx_bytes, rx: traffic.rx_bytes, legacy: false }
        : { kind: "unknown" };
    }
    if (traffic) return telemetryKind(traffic.state, traffic.error_code);

    return { kind: capabilityKind(service.capabilities?.traffic) };
  }

  const capability = capabilityKind(service.capabilities?.traffic);
  if (capability !== "unknown") return { kind: capability };

  return service.stats && isNumber(service.stats.tx) && isNumber(service.stats.rx)
    ? { kind: "ok", tx: service.stats.tx, rx: service.stats.rx, legacy: true }
    : { kind: "unknown" };
}

export function normalizeOnlineClients(service: Service): NormalizedCount {
  const telemetry = service.telemetry;
  if (telemetry) {
    const onlineClients = telemetry.online_clients;
    if (onlineClients?.state === "ok") {
      return isNumber(onlineClients.value)
        ? { kind: "ok", value: onlineClients.value, legacy: false }
        : { kind: "unknown" };
    }
    if (onlineClients) return telemetryKind(onlineClients.state, onlineClients.error_code);

    return { kind: capabilityKind(service.capabilities?.online_clients) };
  }

  return { kind: capabilityKind(service.capabilities?.online_clients) };
}

export function metricLabel(metric: NormalizedTraffic | NormalizedCount): string {
  switch (metric.kind) {
    case "unsupported":
      return "内核不支持";
    case "not_implemented":
      return "探针暂未支持";
    case "not_configured":
      return "需启用统计接口";
    case "disabled":
      return "已禁用";
    case "error":
      return "采集失败";
    case "unknown":
      return "能力未知，请升级探针";
    case "ok":
      return "";
  }
}

export function protocolLabels(service: Service): string[] {
  const protocols = service.protocols;
  if (protocols?.state !== "ok" || !Array.isArray(protocols.items)) return [];

  return protocols.items.includes("vless") ? ["VLESS"] : [];
}
