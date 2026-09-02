import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Enrollment, Release, SessionUser } from "../lib/api";
import { AddAgentPanel } from "./Nodes";

const admin: SessionUser = {
  id: 1,
  username: "admin",
  role: "admin",
  reauthenticated_at: 1_700_000_000,
};

const enrollment: Enrollment = {
  code: "one-use-code",
  expires_at: 1_700_000_600,
  ca_fingerprint: "b".repeat(64),
  release_public_key_hex: "c".repeat(64),
};

const release: Release = {
  id: 1,
  imported_at: 1_700_000_100,
  signature: "sig",
  manifest: {
    version: "v1.2.3",
    os: "linux",
    arch: "amd64",
    byte_size: 123,
    sha256: "a".repeat(64),
    artifact_url: "https://github.com/s2005lg/net-probe/releases/download/v1.2.3/net-probe_linux_amd64",
    minimum_panel_version: "v1.2.3",
    control_version: "1",
    issued_at: 1_700_000_000,
    expires_at: 1_700_003_600,
  },
};

describe("AddAgentPanel", () => {
  it("keeps Agent installation admin-only", () => {
    expect(renderToStaticMarkup(<AddAgentPanel actor={{ ...admin, role: "viewer" }} />)).not.toContain("添加 Agent");
    expect(renderToStaticMarkup(<AddAgentPanel actor={admin} />)).toContain("添加 Agent");
  });

  it("shows a complete one-command installer after enrollment creation", () => {
    const html = renderToStaticMarkup(
      <AddAgentPanel actor={admin} releases={[release]} enrollment={enrollment} panelOrigin="https://panel.example.test:39425" />,
    );

    expect(html).toContain("NET_PROBE_PANEL_URL");
    expect(html).toContain("NET_PROBE_VERSION");
    expect(html).toContain("v1.2.3");
    expect(html).toContain("NET_PROBE_CA_FINGERPRINT");
    expect(html).toContain("NET_PROBE_ENROLLMENT_CODE");
    expect(html).toContain("NET_PROBE_RELEASE_PUBLIC_KEY_HEX");
    expect(html).toContain("https://panel.example.test:39425");
  });

  it("does not render a copyable installer when no signed release is imported", () => {
    const html = renderToStaticMarkup(
      <AddAgentPanel actor={admin} enrollment={enrollment} panelOrigin="https://panel.example.test:39425" />,
    );

    expect(html).toContain("尚未导入此平台的签名发布");
    expect(html).not.toContain("NET_PROBE_VERSION");
    expect(html).not.toContain("<release-tag>");
  });
});
