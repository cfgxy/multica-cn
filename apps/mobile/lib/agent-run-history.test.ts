/**
 * Selector tests for the agent detail 运行历史 section (RUYI-538 ②) — the
 * mobile mirror of web ActivityTab's Recent work filter (`isWorkflowTask` +
 * terminal-with-completed_at + newest-first sort) and its retry admission
 * (run-level issue-scoped endpoint, `failed || cancelled`).
 */
import { describe, expect, it } from "vitest";
import type { AgentTask } from "@multica/core/types";
import {
  isAgentRunRetryable,
  isRunHistoryTask,
  selectAgentRunHistory,
} from "./agent-run-history";

let seq = 0;
function task(overrides: Partial<AgentTask> = {}): AgentTask {
  seq += 1;
  return {
    id: `task-${seq}`,
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "completed",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-10-01T10:00:00Z",
    completed_at: "2026-10-01T10:05:00Z",
    result: null,
    error: null,
    created_at: "2026-10-01T09:59:00Z",
    ...overrides,
  };
}

describe("isRunHistoryTask", () => {
  it("keeps completed / failed / cancelled terminal tasks", () => {
    expect(isRunHistoryTask(task({ status: "completed" }))).toBe(true);
    expect(isRunHistoryTask(task({ status: "failed" }))).toBe(true);
    expect(isRunHistoryTask(task({ status: "cancelled" }))).toBe(true);
  });

  it("drops active tasks even with a stale completed_at", () => {
    expect(isRunHistoryTask(task({ status: "running" }))).toBe(false);
    expect(isRunHistoryTask(task({ status: "queued" }))).toBe(false);
    expect(isRunHistoryTask(task({ status: "cancel_requested" }))).toBe(false);
  });

  it("drops terminal tasks the server has not stamped completed_at on", () => {
    expect(isRunHistoryTask(task({ completed_at: null }))).toBe(false);
  });

  it("hides chat tasks (web isWorkflowTask parity)", () => {
    expect(isRunHistoryTask(task({ chat_session_id: "chat-1" }))).toBe(false);
  });
});

describe("selectAgentRunHistory", () => {
  it("returns only history tasks, newest finished first", () => {
    const oldest = task({ completed_at: "2026-10-01T08:00:00Z" });
    const newest = task({ completed_at: "2026-10-01T12:00:00Z" });
    const middle = task({ completed_at: "2026-10-01T10:00:00Z" });
    const active = task({ status: "running", completed_at: null });
    const chat = task({ chat_session_id: "chat-1" });

    expect(selectAgentRunHistory([oldest, active, newest, chat, middle])).toEqual([
      newest,
      middle,
      oldest,
    ]);
  });

  it("does not mutate the input array", () => {
    const input = [
      task({ completed_at: "2026-10-01T08:00:00Z" }),
      task({ completed_at: "2026-10-01T12:00:00Z" }),
    ];
    const snapshot = [...input];
    selectAgentRunHistory(input);
    expect(input).toEqual(snapshot);
  });

  it("returns an empty list when the agent has no finished runs", () => {
    expect(selectAgentRunHistory([task({ status: "running", completed_at: null })])).toEqual(
      [],
    );
  });
});

describe("isAgentRunRetryable", () => {
  it("admits failed and cancelled issue-linked runs", () => {
    expect(isAgentRunRetryable(task({ status: "failed" }))).toBe(true);
    expect(isAgentRunRetryable(task({ status: "cancelled" }))).toBe(true);
  });

  it("denies completed runs (rerun surface, not retry)", () => {
    expect(isAgentRunRetryable(task({ status: "completed" }))).toBe(false);
  });

  it("denies runs without a linked issue (endpoint is issue-scoped)", () => {
    expect(isAgentRunRetryable(task({ status: "failed", issue_id: "" }))).toBe(
      false,
    );
  });
});
