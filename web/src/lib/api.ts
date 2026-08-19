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
  tags: string[];
  muted_until: number;
  last_report_at: number;
  status: NodeStatus;
  host: Host;
  services: Service[];
  ip_location?: string;
}

export interface NodesResponse {
  items: Node[];
  total: number;
  page: number;
  page_size: number;
}

export interface Tag {
  id: number;
  name: string;
  node_count: number;
}

export function nodeName(node: Node): string {
  return node.alias || node.host.hostname || node.node_id;
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
  hostname: string;
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
  services_json?: string;
}

export interface SettingsData {
  listen_addr: string;
  data_dir: string;
  node_timeout: string;
  admin: { user: string };
  retention: { raw_days: number; hourly_days: number; daily_days: number };
  alert: {
    cert_expiry_days: number;
    disk_usage_pct: number;
    mem_usage_pct: number;
    telegram_token: string;
    telegram_chat_id: string;
    webhook_url: string;
  };
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
  nodes: (params: { q?: string; status?: string; tag?: string; page?: number; page_size?: number } = {}) => {
    const query = new URLSearchParams();
    if (params.q) query.set("q", params.q);
    if (params.status) query.set("status", params.status);
    if (params.tag) query.set("tag", params.tag);
    if (params.page) query.set("page", String(params.page));
    if (params.page_size) query.set("page_size", String(params.page_size));
    const qs = query.toString();
    return request<NodesResponse>(`/nodes${qs ? `?${qs}` : ""}`);
  },
  node: (id: string) => request<Node>(`/nodes/${encodeURIComponent(id)}`),
  patchNode: (
    id: string,
    body: { alias?: string; muted_until?: number; tags?: string[] },
  ) =>
    request<Node>(`/nodes/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  deleteNode: (id: string) =>
    request<{ deleted: boolean }>(`/nodes/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  tags: () => request<Tag[]>("/tags"),
  createTag: (name: string) =>
    request<Tag>("/tags", { method: "POST", body: JSON.stringify({ name }) }),
  deleteTag: (id: number) =>
    request<{ deleted: boolean }>(`/tags/${id}`, { method: "DELETE" }),
  nodeMetrics: (
    id: string,
    granularity?: string,
    range?: { from?: number; to?: number },
  ) => {
    const query = new URLSearchParams();
    if (granularity) query.set("granularity", granularity);
    if (range?.from) query.set("from", String(range.from));
    if (range?.to) query.set("to", String(range.to));
    const qs = query.toString();
    return request<Metric[]>(`/nodes/${encodeURIComponent(id)}/metrics${qs ? `?${qs}` : ""}`);
  },
  alerts: (status?: string, nodeId?: string) => {
    const query = new URLSearchParams();
    if (status) query.set("status", status);
    if (nodeId) query.set("node_id", nodeId);
    const qs = query.toString();
    return request<Alert[]>(`/alerts${qs ? `?${qs}` : ""}`);
  },
  ackAlert: (id: number) =>
    request<{ ok: boolean }>(`/alerts/${id}/ack`, { method: "POST" }),
  versions: () => request<VersionRow[]>("/versions"),
  patchVersion: (serviceType: string, latestVersion: string) =>
    request<VersionRow>(`/versions/${encodeURIComponent(serviceType)}`, {
      method: "PATCH",
      body: JSON.stringify({ latest_version: latestVersion }),
    }),
  settings: () => request<SettingsData>("/settings"),
  patchSettings: (body: {
    node_timeout?: string;
    retention?: { raw_days?: number; hourly_days?: number; daily_days?: number };
    alert?: {
      cert_expiry_days?: number;
      disk_usage_pct?: number;
      mem_usage_pct?: number;
      telegram_token?: string;
      telegram_chat_id?: string;
      webhook_url?: string;
    };
  }) =>
    request<SettingsData>("/settings", {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
};
