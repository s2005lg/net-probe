import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { AgentIdentity, Enrollment, Release, SessionUser } from "../lib/api";
import { AgentsView } from "./Agents";

const admin: SessionUser = {
  id: 1,
  username: "admin",
  role: "admin",
  reauthenticated_at: Math.floor(Date.now() / 1000),
};

const agent: AgentIdentity = {
  agent_id: "agent-1",
  node_id: "node-1",
  display_name: "Tokyo edge",
  control_status: "online",
  last_heartbeat_at: 1_700_000_000,
  agent_version: "v1.2.3",
  os: "linux",
  arch: "amd64",
  certificate_expires_at: 1_800_000_000,
  capabilities: ["upgrade"],
};

const release: Release = {
  id: 1,
  imported_at: 1_700_000_000,
  signature: "signed",
  manifest: {
    version: "v1.2.4",
    os: "linux",
    arch: "amd64",
    byte_size: 900_000,
    sha256: "a".repeat(64),
    artifact_url: "https://github.com/s2005lg/net-probe/releases/download/v1.2.4/net-probe_linux_amd64",
    minimum_panel_version: "v1.2.0",
    control_version: "1",
    issued_at: 1_700_000_000,
    expires_at: 1_700_086_000,
  },
};

const enrollment: Enrollment = {
  code: "one-use-code",
  expires_at: 1_700_000_600,
  ca_fingerprint: "b".repeat(64),
  release_public_key_hex: "c".repeat(64),
};

describe("AgentsView", () => {
  it("keeps enrollment and release workflows admin-only", () => {
    const viewer = renderToStaticMarkup(
      <AgentsView actor={{ ...admin, role: "viewer" }} agents={[agent]} releases={[release]} />,
    );
    expect(viewer).not.toContain("创建一次性注册码");
    expect(viewer).not.toContain("导入签名发布");
    expect(viewer).not.toContain('id="fleet-upgrade-title"');

    const elevated = renderToStaticMarkup(
      <AgentsView actor={admin} agents={[agent]} releases={[release]} />,
    );
    expect(elevated).toContain("创建一次性注册码");
    expect(elevated).toContain("导入签名发布");
    expect(elevated).toContain("批量升级");
  });

  it("reveals the one-use code and explicit signed install inputs only after creation", () => {
    const hidden = renderToStaticMarkup(
      <AgentsView actor={admin} agents={[agent]} releases={[release]} />,
    );
    expect(hidden).not.toContain("NET_PROBE_ENROLLMENT_CODE");

    const visible = renderToStaticMarkup(
      <AgentsView actor={admin} agents={[agent]} releases={[release]} enrollment={enrollment} />,
    );
    expect(visible).toContain("NET_PROBE_ENROLLMENT_CODE");
    expect(visible).toContain("NET_PROBE_CA_FINGERPRINT");
    expect(visible).toContain("NET_PROBE_VERSION");
    expect(visible).toContain("/v1.2.4/install.sh");
    expect(visible).not.toContain("/main/install.sh");
  });

  it("requires recent reauthentication, an exact version, and selected compatible targets", () => {
    const stale = renderToStaticMarkup(
      <AgentsView
        actor={{ ...admin, reauthenticated_at: 0 }}
        agents={[agent]}
        releases={[release]}
        selectedAgentIDs={[agent.agent_id]}
        selectedReleaseID={release.id}
        versionConfirmation="v1.2.4"
      />,
    );
    expect(stale).toContain("需要重新验证密码");
    expect(stale).toMatch(/<button[^>]*data-testid="confirm-upgrade"[^>]*disabled=""[^>]*>/);

    const ready = renderToStaticMarkup(
      <AgentsView
        actor={admin}
        agents={[agent]}
        releases={[release]}
        selectedAgentIDs={[agent.agent_id]}
        selectedReleaseID={release.id}
        versionConfirmation="v1.2.4"
      />,
    );
    expect(ready).toMatch(/<button[^>]*data-testid="confirm-upgrade"(?![^>]* disabled="")[^>]*>/);
  });
});
