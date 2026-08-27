import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Service } from "../lib/api";
import ServiceCard from "./ServiceCard";

function serviceFixture(overrides: Partial<Service> = {}): Service {
  return {
    type: "xray",
    runtime: "systemd",
    active: true,
    enabled: true,
    listen: [],
    listen_ok: true,
    status: "online",
    ...overrides,
  };
}

function xrayFixture(overrides: Partial<Service> = {}): Service {
  return serviceFixture({
    type: "xray",
    capabilities: {
      traffic: { support: "supported" },
      online_clients: { support: "supported" },
    },
    telemetry: {
      traffic: { state: "ok", tx_bytes: 1024, rx_bytes: 2048 },
      online_clients: { state: "ok", value: 3 },
    },
    ...overrides,
  });
}

function anyTLSFixture(): Service {
  return serviceFixture({
    type: "anytls",
    capabilities: {
      traffic: { support: "unsupported", reason_code: "native_api_unavailable" },
      online_clients: { support: "unsupported", reason_code: "native_api_unavailable" },
    },
  });
}

function zeroHysteriaFixture(): Service {
  return serviceFixture({
    type: "hysteria",
    capabilities: {
      traffic: { support: "supported" },
      online_clients: { support: "supported" },
    },
    telemetry: {
      traffic: { state: "ok", tx_bytes: 0, rx_bytes: 0 },
      online_clients: { state: "ok", value: 0 },
    },
  });
}

describe("ServiceCard", () => {
  it("renders VLESS on the host service without a duplicate service", () => {
    const html = renderToStaticMarkup(
      <ServiceCard service={xrayFixture({ protocols: { state: "ok", items: ["vless"], source: "config" } })} />,
    );

    expect(html).toContain("xray");
    expect(html).toContain("VLESS");
    expect((html.match(/VLESS/g) ?? []).length).toBe(1);
  });

  it("renders explicit unavailable and zero values", () => {
    const html = renderToStaticMarkup(<ServiceCard service={anyTLSFixture()} />);
    expect(html).toContain("内核不支持");

    const zero = renderToStaticMarkup(<ServiceCard service={zeroHysteriaFixture()} />);
    expect(zero).toContain("↓0 B ↑0 B");
    expect(zero).toContain("0");
  });

  it("uses a safe error code as supplementary metric detail", () => {
    const html = renderToStaticMarkup(
      <ServiceCard
        service={xrayFixture({
          error: "connection refused: internal host detail",
          telemetry: {
            traffic: { state: "error", error_code: "timeout" },
            online_clients: { state: "error", error_code: "timeout" },
          },
        })}
      />,
    );

    expect(html).toContain("采集失败");
    expect(html).toContain('title="timeout"');
    expect(html).not.toContain("connection refused: internal host detail");
  });
});
