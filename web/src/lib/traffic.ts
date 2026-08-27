import type { Metric, Service } from "./api";
import { normalizeTraffic } from "./serviceTelemetry";

export type TrafficEmptyState = "all_unsupported" | "capabilities_unknown" | "no_valid_data";

export interface TrafficPoint {
  ts: number;
  tx: number | null;
  rx: number | null;
  successful: number;
  eligible: number;
  partial: boolean;
}

export interface TrafficSummary {
  points: TrafficPoint[];
  emptyState: TrafficEmptyState | null;
}

function parseServices(servicesJson?: string): { services: Service[]; unknownCoverage: boolean } {
  if (!servicesJson) return { services: [], unknownCoverage: true };

  try {
    const parsed: unknown = JSON.parse(servicesJson);
    return Array.isArray(parsed)
      ? { services: parsed as Service[], unknownCoverage: false }
      : { services: [], unknownCoverage: true };
  } catch {
    return { services: [], unknownCoverage: true };
  }
}

function isService(value: unknown): value is Service {
  return typeof value === "object" && value !== null;
}

export function aggregateTraffic(metrics: Metric[]): TrafficSummary {
  let successfulServices = 0;
  let eligibleServices = 0;
  let hasUnknownCoverage = false;
  let hasUnsupportedService = false;

  const points = metrics.map((metric) => {
    const { services, unknownCoverage } = parseServices(metric.services_json);
    let tx = 0;
    let rx = 0;
    let successful = 0;
    let eligible = 0;

    hasUnknownCoverage ||= unknownCoverage;

    for (const service of services) {
      if (!isService(service)) {
        hasUnknownCoverage = true;
        continue;
      }

      const traffic = normalizeTraffic(service);
      switch (traffic.kind) {
        case "ok":
          tx += traffic.tx;
          rx += traffic.rx;
          successful += 1;
          eligible += 1;
          break;
        case "not_configured":
        case "disabled":
        case "error":
          eligible += 1;
          break;
        case "unsupported":
        case "not_implemented":
          hasUnsupportedService = true;
          break;
        case "unknown":
          hasUnknownCoverage = true;
          break;
      }
    }

    successfulServices += successful;
    eligibleServices += eligible;

    return {
      ts: metric.ts,
      tx: successful > 0 ? tx : null,
      rx: successful > 0 ? rx : null,
      successful,
      eligible,
      partial: successful > 0 && successful < eligible,
    };
  });

  const emptyState: TrafficEmptyState | null =
    successfulServices > 0
      ? null
      : eligibleServices > 0
        ? "no_valid_data"
        : hasUnknownCoverage
          ? "capabilities_unknown"
          : hasUnsupportedService
            ? "all_unsupported"
            : "no_valid_data";

  return { points, emptyState };
}
