import { describe, expect, it } from "vitest";
import { egressIP } from "./host";
import type { Host } from "./api";

function host(overrides: Partial<Host>): Host {
  return {
    hostname: "n1", os: "Linux", os_version: "", kernel: "", arch: "amd64",
    uptime_seconds: 0, load1: 0, load5: 0, load15: 0,
    mem_total_bytes: 0, mem_available_bytes: 0, mem_used_pct: 0,
    disk_used_pct: 0, upgradable_count: 0,
    ...overrides,
  };
}

describe("egressIP", () => {
  it("prefers discovered IPv4 over all other addresses", () => {
    expect(egressIP(host({ egress_ipv4: "8.8.8.8", egress_ipv6: "2001:4860:4860::8888", ipv4: "10.0.0.2" }))).toBe("8.8.8.8");
  });

  it("falls back through discovered IPv6 and legacy Agent fields", () => {
    expect(egressIP(host({ egress_ipv6: "2001:4860:4860::8888", ipv4: "10.0.0.2" }))).toBe("2001:4860:4860::8888");
    expect(egressIP(host({ ipv4: "1.1.1.1", ipv6: "2001:4860:4860::8888" }))).toBe("1.1.1.1");
    expect(egressIP(host({ ipv6: "2001:4860:4860::8888" }))).toBe("2001:4860:4860::8888");
    expect(egressIP(host({}))).toBe("—");
  });
});
