import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { AgentCommand, AgentIdentity, SessionUser } from "../lib/api";
import AgentControl from "./AgentControl";

function agent(overrides: Partial<AgentIdentity> = {}): AgentIdentity {
  return {
    agent_id: "agent-1",
    node_id: "node-1",
    display_name: "Tokyo edge",
    control_status: "online",
    last_heartbeat_at: 1_700_000_000,
    agent_version: "v1.2.3",
    os: "linux",
    arch: "amd64",
    certificate_expires_at: 1_800_000_000,
    capabilities: ["collect_now", "reload_config", "self_check", "upgrade"],
    ...overrides,
  };
}

function actor(role: SessionUser["role"], reauthenticatedAt = 0): SessionUser {
  return { id: 1, username: "tester", role, reauthenticated_at: reauthenticatedAt };
}

function command(overrides: Partial<AgentCommand> = {}): AgentCommand {
  return {
    command: {
      control_version: "1",
      type: "command",
      command_id: "command-1",
      sequence: 1,
      agent_id: "agent-1",
      action: "collect_now",
      issued_at: 1_700_000_000,
      expires_at: 1_700_000_300,
      payload: {},
      signature: "signature",
    },
    state: "queued",
    dispatched_at: 0,
    accepted_at: 0,
    started_at: 0,
    finished_at: 0,
    attempt_count: 0,
    result_code: "",
    result: {},
    ...overrides,
  };
}

describe("AgentControl", () => {
  it("shows only supported actions", () => {
    const html = renderToStaticMarkup(
      <AgentControl
        agent={agent({ capabilities: ["collect_now"] })}
        actor={actor("admin", Math.floor(Date.now() / 1000))}
        initialCommands={[]}
      />,
    );

    expect(html).toContain("立即采集");
    expect(html).not.toContain("重新加载配置");
    expect(html).not.toContain("运行自检");
    expect(html).not.toContain("升级 Agent");
  });

  it("keeps viewers read-only and operators away from security actions", () => {
    const viewer = renderToStaticMarkup(
      <AgentControl agent={agent()} actor={actor("viewer")} initialCommands={[command()]} />,
    );
    expect(viewer).toContain("命令记录");
    expect(viewer).not.toMatch(/<button[^>]*>立即采集<\/button>/);
    expect(viewer).not.toContain("吊销证书");

    const operator = renderToStaticMarkup(
      <AgentControl agent={agent()} actor={actor("operator")} initialCommands={[]} />,
    );
    expect(operator).toMatch(/<button[^>]*>[^<]*<svg[\s\S]*?立即采集<\/button>/);
    expect(operator).not.toContain("升级 Agent");
    expect(operator).not.toContain("吊销证书");
  });

  it("shows queued expiry and maps bounded result codes", () => {
    const html = renderToStaticMarkup(
      <AgentControl
        agent={agent({ control_status: "offline" })}
        actor={actor("viewer")}
        initialCommands={[
          command(),
          command({
            state: "failed",
            result_code: "self_check_failed",
            finished_at: 1_700_000_010,
          }),
        ]}
      />,
    );
    expect(html).toContain("离线等待");
    expect(html).toContain("过期于");
    expect(html).toContain("自检未通过");
  });
});
