export interface Host {
  hostname: string;
  os: string;
  os_version: string;
  kernel: string;
  arch: string;
  ipv4?: string;
  ipv6?: string;
  uptime_seconds: number;
  load1: number;
  load5: number;
  load15: number;
  mem_total_bytes: number;
  mem_available_bytes: number;
  mem_used_pct: number;
  disk_total_bytes?: number;
  disk_used_bytes?: number;
  disk_total_human?: string;
  disk_used_human?: string;
  disk_used_pct: number;
  upgradable_count: number;
}

export interface Listen {
  proto: string;
  addr: string;
  port: number;
}

export interface Cert {
  not_after: string;
  days_left: number;
}

export interface ServiceStats {
  tx: number;
  rx: number;
  online_clients?: number;
}

export interface Service {
  type: string;
  runtime: string;
  unit?: string;
  binary?: string;
  version?: string;
  active: boolean;
  enabled: boolean;
  main_pid?: number;
  n_restarts?: number;
  listen: Listen[];
  listen_ok: boolean;
  cert?: Cert | null;
  stats?: ServiceStats | null;
  status: string;
  error?: string;
}

export type NodeStatus = "online" | "offline";

export interface Node {
  node_id: string;
  alias: string;
  muted_until: number;
  last_report_at: number;
  status: NodeStatus;
  host: Host;
  services: Service[];
}

export interface ServiceDist {
  type: string;
  count: number;
}

export interface Overview {
  nodes_total: number;
  nodes_online: number;
  alerts_active: number;
  services_total: number;
  service_distribution: ServiceDist[];
}

export type AlertStatus = "firing" | "recovered" | "acknowledged";

export interface Alert {
  id: number;
  node_id: string;
  rule: string;
  status: AlertStatus;
  message: string;
  first_seen_at: number;
  last_seen_at: number;
  recovered_at: number;
  acknowledged_at: number;
}

export interface VersionRow {
  service_type: string;
  latest_version: string;
  source: string;
  updated_at: number;
}

export interface Metric {
  ts: number;
  granularity: string;
  load1: number;
  load5: number;
  load15: number;
  mem_used_pct: number;
  disk_used_pct: number;
}

export interface SettingsData {
  listen_addr: string;
  data_dir: string;
  node_timeout: string;
  admin: { user: string };
  retention: { raw_days: number; hourly_days: number; daily_days: number };
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1/admin${path}`, {
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (res.status === 401 && !path.startsWith("/login")) {
    window.location.replace("/login");
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    let message = res.statusText;
    try {
      const data = await res.json();
      message = data?.error?.message ?? data?.error?.code ?? message;
    } catch {
      // keep statusText
    }
    throw new Error(message);
  }
  return res.json() as Promise<T>;
}

export const api = {
  login: (username: string, password: string) =>
    request<{ ok: boolean }>("/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<{ ok: boolean }>("/logout", { method: "POST" }),
  overview: () => request<Overview>("/overview"),
  nodes: () => request<Node[]>("/nodes"),
  node: (id: string) => request<Node>(`/nodes/${encodeURIComponent(id)}`),
  nodeMetrics: (id: string, granularity?: string) =>
    request<Metric[]>(
      `/nodes/${encodeURIComponent(id)}/metrics${
        granularity ? `?granularity=${encodeURIComponent(granularity)}` : ""
      }`,
    ),
  alerts: (status?: string) =>
    request<Alert[]>(`/alerts${status ? `?status=${encodeURIComponent(status)}` : ""}`),
  ackAlert: (id: number) =>
    request<{ ok: boolean }>(`/alerts/${id}/ack`, { method: "POST" }),
  versions: () => request<VersionRow[]>("/versions"),
  patchVersion: (serviceType: string, latestVersion: string) =>
    request<VersionRow>(`/versions/${encodeURIComponent(serviceType)}`, {
      method: "PATCH",
      body: JSON.stringify({ latest_version: latestVersion }),
    }),
  settings: () => request<SettingsData>("/settings"),
};
