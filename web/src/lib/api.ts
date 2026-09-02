export interface Host {
  hostname: string;
  os: string;
  os_version: string;
  kernel: string;
  arch: string;
  ipv4?: string;
  ipv6?: string;
  egress_ipv4?: string;
  egress_ipv6?: string;
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

export type ProtocolState = "ok" | "unknown" | "error";
export type CapabilitySupport = "supported" | "unsupported" | "unknown";
export type CapabilityReasonCode =
  | "native_api_unavailable"
  | "collector_not_implemented"
  | "agent_too_old";
export type ObservationState = "ok" | "not_configured" | "disabled" | "error";
export type TelemetryErrorCode =
  | "timeout"
  | "unauthorized"
  | "connection_failed"
  | "invalid_response"
  | "command_failed"
  | "config_unreadable"
  | "unknown";

export interface ProtocolInfo {
  state: ProtocolState;
  items: string[];
  source?: string;
}

export interface MetricCapability {
  support: CapabilitySupport;
  source?: string;
  reason_code?: CapabilityReasonCode;
}

export interface ServiceCapabilities {
  traffic: MetricCapability;
  online_clients: MetricCapability;
}

export interface TrafficTelemetry {
  state: ObservationState;
  tx_bytes?: number;
  rx_bytes?: number;
  error_code?: TelemetryErrorCode;
}

export interface CountTelemetry {
  state: ObservationState;
  value?: number;
  error_code?: TelemetryErrorCode;
}

export interface ServiceTelemetry {
  traffic?: TrafficTelemetry;
  online_clients?: CountTelemetry;
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
  listen: Listen[] | null;
  listen_ok: boolean;
  cert?: Cert | null;
  stats?: ServiceStats | null;
  protocols?: ProtocolInfo | null;
  capabilities?: ServiceCapabilities | null;
  telemetry?: ServiceTelemetry | null;
  status: string;
  error?: string;
}

export type NodeStatus = "online" | "offline";
export type ControlStatus = "online" | "offline";
export type Role = "viewer" | "operator" | "admin";
export type AgentAction = "collect_now" | "reload_config" | "self_check" | "upgrade";
export type CommandState =
  | "queued"
  | "dispatched"
  | "accepted"
  | "running"
  | "succeeded"
  | "failed"
  | "expired";

export interface SessionUser {
  id: number;
  username: string;
  role: Role;
  reauthenticated_at: number;
}

export interface AgentIdentity {
  agent_id: string;
  node_id: string;
  display_name: string;
  control_status: ControlStatus;
  last_heartbeat_at: number;
  agent_version: string;
  os: string;
  arch: string;
  certificate_expires_at: number;
  capabilities: AgentAction[];
}

export interface CommandEnvelope {
  control_version: "1";
  type: "command";
  command_id: string;
  sequence: number;
  agent_id: string;
  action: AgentAction;
  issued_at: number;
  expires_at: number;
  payload: Record<string, unknown>;
  signature: string;
}

export interface AgentCommand {
  command: CommandEnvelope;
  state: CommandState;
  dispatched_at: number;
  accepted_at: number;
  started_at: number;
  finished_at: number;
  attempt_count: number;
  result_code: string;
  result: Record<string, unknown>;
}

export interface Enrollment {
  code: string;
  expires_at: number;
  ca_fingerprint: string;
  release_public_key_hex: string;
}

export interface ReleaseManifest {
  version: string;
  os: string;
  arch: string;
  byte_size: number;
  sha256: string;
  artifact_url: string;
  minimum_panel_version: string;
  control_version: "1";
  issued_at: number;
  expires_at: number;
}

export interface Release {
  id: number;
  manifest: ReleaseManifest;
  signature: string;
  imported_at: number;
}

export type ReleaseImportResponse = Release;

export interface Node {
  node_id: string;
  alias: string;
  tags: string[];
  muted_until: number;
  last_report_at: number;
  status: NodeStatus;
  host: Host;
  services: Service[];
  effective_ip?: string;
  ip_location?: string;
  agent_id: string;
  control_status: ControlStatus;
  last_heartbeat_at: number;
  agent_version: string;
  agent_os: string;
  agent_arch: string;
  certificate_expires_at: number;
  agent_capabilities: AgentAction[];
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

export function agentFromNode(node: Node): AgentIdentity | null {
  if (!node.agent_id) return null;
  return {
    agent_id: node.agent_id,
    node_id: node.node_id,
    display_name: nodeName(node),
    control_status: node.control_status,
    last_heartbeat_at: node.last_heartbeat_at,
    agent_version: node.agent_version,
    os: node.agent_os,
    arch: node.agent_arch,
    certificate_expires_at: node.certificate_expires_at,
    capabilities: node.agent_capabilities,
  };
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
  alias: string;
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
  services_json?: Service[] | string;
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
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const api = {
  login: (username: string, password: string) =>
    request<{ ok: boolean }>("/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<{ ok: boolean }>("/logout", { method: "POST" }),
  me: () => request<SessionUser>("/me"),
  reauthenticate: (password: string) =>
    request<{ ok: boolean }>("/reauth", {
      method: "POST",
      body: JSON.stringify({ password }),
    }),
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
  allNodes: async () => {
    const first = await api.nodes({ page: 1, page_size: 100 });
    const pages = Math.ceil(first.total / first.page_size);
    if (pages <= 1) return first.items;
    const rest = await Promise.all(
      Array.from({ length: pages - 1 }, (_, index) =>
        api.nodes({ page: index + 2, page_size: 100 }),
      ),
    );
    return [first, ...rest].flatMap((page) => page.items);
  },
  commandHistory: (agentID: string) =>
    request<{ items: AgentCommand[] }>(`/agents/${encodeURIComponent(agentID)}/commands`),
  createCommand: (agentID: string, action: Exclude<AgentAction, "upgrade">, payload: Record<string, unknown>) =>
    request<{ command: CommandEnvelope; state: CommandState }>(
      `/agents/${encodeURIComponent(agentID)}/commands`,
      { method: "POST", body: JSON.stringify({ action, payload }) },
    ),
  createEnrollment: (label: string) =>
    request<Enrollment>("/enrollments", {
      method: "POST",
      body: JSON.stringify({ label, expires_in_seconds: 600 }),
    }),
  revokeAgent: (agentID: string) =>
    request<void>(`/agents/${encodeURIComponent(agentID)}/revoke`, { method: "POST" }),
  releases: () => request<{ items: Release[] }>("/releases"),
  importRelease: (manifest: ReleaseManifest, signature: string) =>
    request<ReleaseImportResponse>("/releases", {
      method: "POST",
      body: JSON.stringify({ manifest, signature }),
    }),
  importGitHubRelease: (input: { version: string; os: string; arch: string }) =>
    request<ReleaseImportResponse>("/releases/github", {
      method: "POST",
      body: JSON.stringify(input),
    }),
  createUpgrades: (input: {
    version: string;
    os: string;
    arch: string;
    agent_ids: string[];
    confirmed: true;
  }) => request<{ commands: CommandEnvelope[] }>("/upgrades", { method: "POST", body: JSON.stringify(input) }),
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
