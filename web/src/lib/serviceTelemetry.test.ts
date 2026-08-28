import { describe, expect, it } from "vitest";
import type { Service } from "./api";
import { metricLabel, normalizeOnlineClients, normalizeTraffic, protocolLabels } from "./serviceTelemetry";

function fixture(overrides: Partial<Service> = {}): Service {
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

describe("service telemetry compatibility", () => {
  it("preserves a successful zero", () => {
    const service = fixture({
      capabilities: { traffic: { support: "supported" }, online_clients: { support: "supported" } },
      telemetry: { traffic: { state: "ok", tx_bytes: 0, rx_bytes: 0 } },
    });

    expect(normalizeTraffic(service)).toMatchObject({ kind: "ok", tx: 0, rx: 0 });
  });

  it("preserves a successful zero online-client count", () => {
    const service = fixture({
      capabilities: { traffic: { support: "supported" }, online_clients: { support: "supported" } },
      telemetry: { online_clients: { state: "ok", value: 0 } },
    });

    expect(normalizeOnlineClients(service)).toEqual({ kind: "ok", value: 0, legacy: false });
  });

  it("does not use legacy stats when telemetry omits a metric", () => {
    const service = fixture({
      stats: { tx: 7, rx: 9, online_clients: 3 },
      capabilities: { traffic: { support: "supported" }, online_clients: { support: "supported" } },
      telemetry: {},
    });

    expect(normalizeTraffic(service)).toEqual({ kind: "unknown" });
    expect(normalizeOnlineClients(service)).toEqual({ kind: "unknown" });
  });

  it("normalizes collector-not-implemented capabilities", () => {
    const service = fixture({
      capabilities: {
        traffic: { support: "unsupported", reason_code: "collector_not_implemented" },
        online_clients: { support: "unsupported", reason_code: "collector_not_implemented" },
      },
    });

    expect(normalizeTraffic(service)).toEqual({ kind: "not_implemented" });
    expect(normalizeOnlineClients(service)).toEqual({ kind: "not_implemented" });
  });

  it("labels native unavailability separately", () => {
    const service = fixture({
      capabilities: {
        traffic: { support: "unsupported", reason_code: "native_api_unavailable" },
        online_clients: { support: "unsupported", reason_code: "native_api_unavailable" },
      },
    });

    expect(metricLabel(normalizeTraffic(service))).toBe("内核不支持");
  });

  it("does not trust legacy online_clients zero", () => {
    const service = fixture({ stats: { tx: 7, rx: 9, online_clients: 0 } });

    expect(normalizeTraffic(service).kind).toBe("ok");
    expect(normalizeOnlineClients(service).kind).toBe("unknown");
  });

  it("returns the VLESS label", () => {
    const service = fixture({ protocols: { state: "ok", items: ["vless"], source: "config" } });

    expect(protocolLabels(service)).toEqual(["VLESS"]);
  });

  it("does not fall back to legacy traffic when telemetry reports an error", () => {
    const service = fixture({
      stats: { tx: 7, rx: 9, online_clients: 3 },
      capabilities: { traffic: { support: "supported" }, online_clients: { support: "supported" } },
      telemetry: { traffic: { state: "error", error_code: "timeout" } },
    });

    expect(normalizeTraffic(service)).toEqual({ kind: "error", errorCode: "timeout" });
  });

  it("maps every supported observation state to its approved label", () => {
    expect(metricLabel({ kind: "not_implemented" })).toBe("探针暂未支持");
    expect(metricLabel({ kind: "not_configured" })).toBe("需启用统计接口");
    expect(metricLabel({ kind: "disabled" })).toBe("已禁用");
    expect(metricLabel({ kind: "error", errorCode: "timeout" })).toBe("采集失败");
    expect(metricLabel({ kind: "unknown" })).toBe("能力未知，请升级探针");
  });

  it("treats unknown contract values as unknown", () => {
    const service = fixture({
      capabilities: {
        traffic: { support: "unsupported", reason_code: "future_reason" as never },
        online_clients: { support: "future_support" as never },
      },
      protocols: { state: "future_state" as never, items: ["vless"] },
    });

    expect(normalizeTraffic(service).kind).toBe("unknown");
    expect(normalizeOnlineClients(service).kind).toBe("unknown");
    expect(protocolLabels(service)).toEqual([]);
  });

  it("only displays the exact lower-case VLESS protocol item", () => {
    const service = fixture({ protocols: { state: "ok", items: ["VLESS", "vless2", "vless", "vless"] } });

    expect(protocolLabels(service)).toEqual(["VLESS"]);
  });

  it("ignores malformed protocol items", () => {
    const service = fixture({ protocols: { state: "ok", items: "vless" as never } });

    expect(protocolLabels(service)).toEqual([]);
  });
});
