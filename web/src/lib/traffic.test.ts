import { describe, expect, it } from "vitest";
import type { Metric, Service } from "./api";
import { aggregateTraffic } from "./traffic";

function service(overrides: Partial<Service> = {}): Service {
  return {
    type: "xray",
    runtime: "systemd",
    active: true,
    enabled: true,
    listen: [],
    listen_ok: true,
    status: "running",
    ...overrides,
  };
}

function serviceWithTraffic(tx: number, rx: number): Service {
  return service({
    capabilities: { traffic: { support: "supported" }, online_clients: { support: "supported" } },
    telemetry: { traffic: { state: "ok", tx_bytes: tx, rx_bytes: rx } },
  });
}

function serviceWithTrafficError(errorCode: "timeout" = "timeout"): Service {
  return service({
    capabilities: { traffic: { support: "supported" }, online_clients: { support: "supported" } },
    telemetry: { traffic: { state: "error", error_code: errorCode } },
  });
}

function unsupportedService(): Service {
  return service({
    capabilities: {
      traffic: { support: "unsupported", reason_code: "native_api_unavailable" },
      online_clients: { support: "unsupported", reason_code: "native_api_unavailable" },
    },
  });
}

function legacyServiceWithoutStats(): Service {
  return service();
}

function metric(ts: number, services: Service[]): Metric {
  return {
    ts,
    granularity: "raw",
    load1: 0,
    load5: 0,
    load15: 0,
    mem_used_pct: 0,
    disk_used_pct: 0,
    services_json: JSON.stringify(services),
  };
}

describe("aggregateTraffic", () => {
  it("keeps an all-zero successful point", () => {
    const summary = aggregateTraffic([metric(1, [serviceWithTraffic(0, 0)])]);

    expect(summary.points).toEqual([
      { ts: 1, tx: 0, rx: 0, successful: 1, eligible: 1, partial: false },
    ]);
    expect(summary.emptyState).toBeNull();
  });

  it("uses null for collection gaps", () => {
    const summary = aggregateTraffic([metric(1, [serviceWithTrafficError("timeout")])]);

    expect(summary.points[0]).toMatchObject({ tx: null, rx: null, successful: 0, eligible: 1 });
    expect(summary.emptyState).toBe("no_valid_data");
  });

  it("marks partial aggregates", () => {
    const summary = aggregateTraffic([
      metric(1, [serviceWithTraffic(7, 9), serviceWithTrafficError("timeout")]),
    ]);

    expect(summary.points[0]).toMatchObject({
      tx: 7,
      rx: 9,
      successful: 1,
      eligible: 2,
      partial: true,
    });
  });

  it("distinguishes unsupported from unknown", () => {
    expect(aggregateTraffic([metric(1, [unsupportedService()])]).emptyState).toBe("all_unsupported");
    expect(aggregateTraffic([metric(1, [legacyServiceWithoutStats()])]).emptyState).toBe(
      "capabilities_unknown",
    );
  });

  it("treats malformed service snapshots as unknown coverage", () => {
    const summary = aggregateTraffic([{ ...metric(1, []), services_json: "{" }]);

    expect(summary.points).toEqual([
      { ts: 1, tx: null, rx: null, successful: 0, eligible: 0, partial: false },
    ]);
    expect(summary.emptyState).toBe("capabilities_unknown");
  });
});
