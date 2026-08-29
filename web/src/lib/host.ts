import type { Host } from "./api";

export function egressIP(host: Host): string {
  return host.egress_ipv4 || host.egress_ipv6 || host.ipv4 || host.ipv6 || "—";
}
