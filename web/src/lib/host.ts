import type { Host } from "./api";

export function egressIP(host: Host, effectiveIP?: string): string {
  if (effectiveIP !== undefined) return effectiveIP || "—";
  return host.egress_ipv4 || host.egress_ipv6 || host.ipv4 || host.ipv6 || "—";
}
