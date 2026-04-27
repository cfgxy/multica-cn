import { describe, expect, it } from "vitest";
import type { Agent, AgentRuntime, AgentTask, TaskFailureReason } from "../types";
import {
  FAILED_WINDOW_MS,
  deriveAgentPresence,
  deriveAgentPresenceDetail,
} from "./derive-presence";

const FIXED_NOW = new Date("2026-04-27T12:00:00Z").getTime();

function makeAgent(): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    name: "Test Agent",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_env: {},
    custom_args: [],
    custom_env_redacted: false,
    visibility: "workspace",
    status: "idle",
    max_concurrent_tasks: 6,
    model: "",
    owner_id: null,
    skills: [],
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  };
}

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Test Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: null,
    last_seen_at: new Date(FIXED_NOW - 10_000).toISOString(),
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}

function makeTask(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "rt-1",
    issue_id: "",
    status: "queued",
    priority: 0,
    dispatched_at: null,
    started_at: null,
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-04-27T11:00:00Z",
    ...overrides,
  };
}

describe("deriveAgentPresence", () => {
  it("returns offline when runtime is offline (even if a task is running)", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime({ status: "offline" }),
        recentTasks: [makeTask({ status: "running" })],
        now: FIXED_NOW,
      }),
    ).toBe("offline");
  });

  it("returns failed when a task failed within the recent window", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [
          makeTask({
            status: "failed",
            completed_at: new Date(FIXED_NOW - 30_000).toISOString(), // 30s ago
          }),
        ],
        now: FIXED_NOW,
      }),
    ).toBe("failed");
  });

  it("ignores failed tasks older than the recent window", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [
          makeTask({
            status: "failed",
            completed_at: new Date(FIXED_NOW - 5 * 60_000).toISOString(), // 5min ago
          }),
        ],
        now: FIXED_NOW,
      }),
    ).toBe("available");
  });

  it("returns working when at least one task is running", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [makeTask({ status: "running" })],
        now: FIXED_NOW,
      }),
    ).toBe("working");
  });

  it("returns pending when only queued tasks exist", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [makeTask({ status: "queued" })],
        now: FIXED_NOW,
      }),
    ).toBe("pending");
  });

  it("returns pending when only dispatched tasks exist", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [makeTask({ status: "dispatched" })],
        now: FIXED_NOW,
      }),
    ).toBe("pending");
  });

  it("returns available when runtime is online and no tasks are present", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [],
        now: FIXED_NOW,
      }),
    ).toBe("available");
  });

  it("treats missing completed_at on a failed task as not-recent (ignored)", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [makeTask({ status: "failed", completed_at: null })],
        now: FIXED_NOW,
      }),
    ).toBe("available");
  });

  it("respects the FAILED_WINDOW_MS boundary just inside the window", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [
          makeTask({
            status: "failed",
            completed_at: new Date(FIXED_NOW - (FAILED_WINDOW_MS - 1_000)).toISOString(),
          }),
        ],
        now: FIXED_NOW,
      }),
    ).toBe("failed");
  });

  it("respects the FAILED_WINDOW_MS boundary just outside the window", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [
          makeTask({
            status: "failed",
            completed_at: new Date(FIXED_NOW - (FAILED_WINDOW_MS + 1_000)).toISOString(),
          }),
        ],
        now: FIXED_NOW,
      }),
    ).toBe("available");
  });

  it("prioritizes failed over working when both exist", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [
          makeTask({ status: "running" }),
          makeTask({
            id: "task-2",
            status: "failed",
            completed_at: new Date(FIXED_NOW - 30_000).toISOString(),
          }),
        ],
        now: FIXED_NOW,
      }),
    ).toBe("failed");
  });

  it("prioritizes working over pending when both exist", () => {
    expect(
      deriveAgentPresence({
        agent: makeAgent(),
        runtime: makeRuntime(),
        recentTasks: [
          makeTask({ status: "running" }),
          makeTask({ id: "task-2", status: "queued" }),
        ],
        now: FIXED_NOW,
      }),
    ).toBe("working");
  });
});

describe("deriveAgentPresenceDetail", () => {
  it("counts running and queued tasks separately", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime(),
      recentTasks: [
        makeTask({ status: "running" }),
        makeTask({ id: "task-2", status: "queued" }),
        makeTask({ id: "task-3", status: "dispatched" }),
      ],
      now: FIXED_NOW,
    });
    expect(detail.presence).toBe("working");
    expect(detail.runningCount).toBe(1);
    expect(detail.queuedCount).toBe(2);
  });

  it("only counts tasks belonging to the target agent", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime(),
      recentTasks: [
        makeTask({ status: "running" }),
        makeTask({ id: "task-2", agent_id: "other-agent", status: "running" }),
      ],
      now: FIXED_NOW,
    });
    expect(detail.runningCount).toBe(1);
  });

  it("surfaces failure_reason on the failed task", () => {
    const reason: TaskFailureReason = "runtime_offline";
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime(),
      recentTasks: [
        makeTask({
          status: "failed",
          completed_at: new Date(FIXED_NOW - 30_000).toISOString(),
          failure_reason: reason,
        }),
      ],
      now: FIXED_NOW,
    });
    expect(detail.presence).toBe("failed");
    expect(detail.failureReason).toBe(reason);
  });

  it("leaves failureReason undefined when failed task has empty failure_reason", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime(),
      recentTasks: [
        makeTask({
          status: "failed",
          completed_at: new Date(FIXED_NOW - 30_000).toISOString(),
          failure_reason: "",
        }),
      ],
      now: FIXED_NOW,
    });
    expect(detail.presence).toBe("failed");
    expect(detail.failureReason).toBeUndefined();
  });

  it("leaves failureReason undefined when not in failed state", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime(),
      recentTasks: [makeTask({ status: "running" })],
      now: FIXED_NOW,
    });
    expect(detail.failureReason).toBeUndefined();
  });
});
