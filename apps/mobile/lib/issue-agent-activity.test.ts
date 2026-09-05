/**
 * Pure-selector tests for the workspace agent-task snapshot slices that
 * power the "agent is working on this issue" cue (inbox rows, RUYI-76 ③)
 * and the per-agent running-task view (more/agents, RUYI-76 ②).
 *
 * Semantics mirrored from web `packages/views/issues/surface/activity.ts`
 * (`selectIssueTasks` / `isQueuedTaskStatus`) — the same-status-bucketing
 * parity rule in apps/mobile/CLAUDE.md.
 */
import { describe, expect, it } from "vitest";
import type { AgentTask } from "@multica/core/types";
import {
  deriveIssueActivityMap,
  isActiveTaskStatus,
  selectAgentActiveTasks,
  selectIssueActivity,
} from "./issue-agent-activity";

let seq = 0;
function task(overrides: Partial<AgentTask> = {}): AgentTask {
  seq += 1;
  return {
    id: `task-${seq}`,
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "running",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-09-05T10:00:00Z",
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-09-05T09:59:00Z",
    ...overrides,
  } as AgentTask;
}

describe("isActiveTaskStatus", () => {
  it("treats queued/dispatched/waiting_local_directory/running as active", () => {
    expect(isActiveTaskStatus("queued")).toBe(true);
    expect(isActiveTaskStatus("dispatched")).toBe(true);
    expect(isActiveTaskStatus("waiting_local_directory")).toBe(true);
    expect(isActiveTaskStatus("running")).toBe(true);
  });

  it("treats terminal statuses as inactive", () => {
    expect(isActiveTaskStatus("completed")).toBe(false);
    expect(isActiveTaskStatus("failed")).toBe(false);
    expect(isActiveTaskStatus("cancelled")).toBe(false);
  });
});

describe("selectIssueActivity", () => {
  it("buckets one issue's tasks into running vs queued", () => {
    const running = task({ status: "running" });
    const dispatched = task({ status: "dispatched" });
    const parked = task({ status: "waiting_local_directory" });
    const queued = task({ status: "queued" });

    const groups = selectIssueActivity(
      [running, dispatched, parked, queued],
      "issue-1",
    );

    expect(groups.running).toEqual([running]);
    expect(groups.queued).toEqual([dispatched, parked, queued]);
  });

  it("drops terminal tasks and other issues' tasks", () => {
    const done = task({ status: "completed" });
    const failed = task({ status: "failed" });
    const otherIssue = task({ issue_id: "issue-2" });

    const groups = selectIssueActivity([done, failed, otherIssue], "issue-1");

    expect(groups.running).toEqual([]);
    expect(groups.queued).toEqual([]);
  });

  it("never matches chat/autopilot tasks with no issue", () => {
    const chatTask = task({ issue_id: "", chat_session_id: "chat-1" });
    const groups = selectIssueActivity([chatTask], "issue-1");
    expect(groups.running).toEqual([]);
    expect(groups.queued).toEqual([]);
  });
});

describe("deriveIssueActivityMap", () => {
  it("maps every issue that has at least one active task", () => {
    const aRunning = task({ issue_id: "issue-a" });
    const bQueued = task({ issue_id: "issue-b", status: "queued" });
    const map = deriveIssueActivityMap([aRunning, bQueued]);

    expect(map.get("issue-a")?.running).toEqual([aRunning]);
    expect(map.get("issue-a")?.queued).toEqual([]);
    expect(map.get("issue-b")?.queued).toEqual([bQueued]);
  });

  it("omits issues whose tasks are all terminal and skips issue-less tasks", () => {
    const done = task({ status: "completed" });
    const chatTask = task({ issue_id: "" });
    const map = deriveIssueActivityMap([done, chatTask]);

    expect(map.size).toBe(0);
  });
});

describe("selectAgentActiveTasks", () => {
  it("returns only that agent's active tasks, terminal dropped", () => {
    const mine = task({ agent_id: "agent-1" });
    const others = task({ agent_id: "agent-2" });
    const mineDone = task({ agent_id: "agent-1", status: "completed" });

    const out = selectAgentActiveTasks([mine, others, mineDone], "agent-1");
    expect(out).toEqual([mine]);
  });

  it("orders running first, then queued — newest first within each group", () => {
    const olderRunning = task({
      status: "running",
      started_at: "2026-09-05T10:00:00Z",
      created_at: "2026-09-05T09:59:00Z",
    });
    const newerRunning = task({
      status: "running",
      started_at: "2026-09-05T10:05:00Z",
      created_at: "2026-09-05T10:04:00Z",
    });
    const newerQueued = task({
      status: "queued",
      started_at: null,
      created_at: "2026-09-05T10:06:00Z",
    });
    const olderQueued = task({
      status: "dispatched",
      dispatched_at: "2026-09-05T10:01:00Z",
      created_at: "2026-09-05T10:00:30Z",
    });

    const out = selectAgentActiveTasks(
      [olderQueued, newerQueued, olderRunning, newerRunning],
      "agent-1",
    );

    expect(out).toEqual([newerRunning, olderRunning, newerQueued, olderQueued]);
  });

  it("includes issue-less chat/autopilot runs — they are the agent's runs too", () => {
    const chatRun = task({ issue_id: "", chat_session_id: "chat-1" });
    const out = selectAgentActiveTasks([chatRun], "agent-1");
    expect(out).toEqual([chatRun]);
  });
});
