import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Enrollment, SessionUser } from "../lib/api";
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

describe("AddAgentPanel", () => {
  it("keeps Agent installation admin-only", () => {
    expect(renderToStaticMarkup(<AddAgentPanel actor={{ ...admin, role: "viewer" }} />)).not.toContain("添加 Agent");
    expect(renderToStaticMarkup(<AddAgentPanel actor={admin} />)).toContain("添加 Agent");
  });

  it("shows a complete one-command installer after enrollment creation", () => {
    const html = renderToStaticMarkup(
      <AddAgentPanel actor={admin} enrollment={enrollment} panelOrigin="https://panel.example.test:39425" />,
    );

    expect(html).toContain("NET_PROBE_PANEL_URL");
    expect(html).toContain("NET_PROBE_VERSION");
    expect(html).toContain("NET_PROBE_CA_FINGERPRINT");
    expect(html).toContain("NET_PROBE_ENROLLMENT_CODE");
    expect(html).toContain("NET_PROBE_RELEASE_PUBLIC_KEY_HEX");
    expect(html).toContain("https://panel.example.test:39425");
  });
});
