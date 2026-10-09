/**
 * Query-module tests for the per-agent task list (RUYI-538 ②) — key shape,
 * enabled gating, and the endpoint the queryFn dials. The api layer is
 * mocked so the native fetch chain never loads (vitest lane contract).
 */
import { describe, expect, it, vi } from "vitest";

const mockListAgentTasks = vi.fn();

vi.mock("@/data/api", () => ({
  api: {
    listAgentTasks: (...args: unknown[]) => mockListAgentTasks(...args),
  },
}));

import { agentTasksKeys, agentTasksOptions } from "./agent-tasks";

describe("agentTasksKeys", () => {
  it("scopes the family under the workspace, detail under the agent", () => {
    expect(agentTasksKeys.all("ws-1")).toEqual(["agent-tasks", "ws-1"]);
    expect(agentTasksKeys.detail("ws-1", "agent-1")).toEqual([
      "agent-tasks",
      "ws-1",
      "agent-1",
    ]);
  });
});

describe("agentTasksOptions", () => {
  it("dials GET listAgentTasks with the agent id", () => {
    mockListAgentTasks.mockResolvedValue([]);
    const options = agentTasksOptions("ws-1", "agent-1");
    expect(options.enabled).toBe(true);
    return Promise.resolve(options.queryFn?.({ signal: undefined } as never)).then(
      () => {
        expect(mockListAgentTasks).toHaveBeenCalledWith("agent-1", {
          signal: undefined,
        });
      },
    );
  });

  it("stays disabled without a workspace or an agent id", () => {
    expect(agentTasksOptions(null, "agent-1").enabled).toBe(false);
    expect(agentTasksOptions("ws-1", "").enabled).toBe(false);
  });
});
