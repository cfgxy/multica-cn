import { describe, expect, it } from "vitest";

import { MulticaApiError } from "../src/rest.js";
import { findTool, TOOL_DEFINITIONS } from "../src/tools.js";
import type { MulticaClient } from "../src/rest.js";
import { ToolInputError } from "../src/schemas.js";
import type { IssueInfo } from "../src/types.js";

const WS = "voice-notes";

function fakeClient(overrides: Partial<Record<string, unknown>> = {}): MulticaClient {
  const calls: Array<{ method: string; args: unknown[] }> = [];
  const base = {
    listWorkspaces: async () => [
      { id: "w1", slug: WS, name: "Voice", issue_prefix: "VOI" },
    ],
    listAgents: async () => [{ id: "a1", name: "顾小鱼", runtime_bound: true }],
    listProjects: async () => ({
      projects: [{ id: "p1", title: "Playground", status: "active", issue_count: 3 }],
      total: 1,
    }),
    listIssues: async () => ({ issues: [], total: 0 }),
    getIssue: async () => issueFixture(),
    listComments: async () => [],
    searchIssues: async () => ({ issues: [], total: 0 }),
    createIssue: async (ws: string, body: Record<string, unknown>) => {
      calls.push({ method: "createIssue", args: [ws, body] });
      return { ...issueFixture(), title: String(body.title) };
    },
    addComment: async () => ({ id: "c1", content: "x", trigger_outcomes: [] }),
    updateIssue: async (_ws: string, id: string, body: Record<string, unknown>) => {
      calls.push({ method: "updateIssue", args: [id, body] });
      return issueFixture({ status: "in_progress", revision: 3 });
    },
    quickCreateIssue: async (ws: string, body: Record<string, unknown>) => {
      calls.push({ method: "quickCreateIssue", args: [ws, body] });
      return { task_id: "task-1" };
    },
  };
  const merged = { ...base, ...overrides } as unknown;
  const client = merged as MulticaClient;
  (client as unknown as { __calls: unknown }).__calls = calls;
  return client;
}

function issueFixture(over: Partial<IssueInfo> = {}): IssueInfo {
  return {
    id: "i1",
    identifier: "VOI-1",
    number: 1,
    title: "First issue",
    status: "todo",
    revision: 1,
    ...over,
  };
}

function callsOf(client: MulticaClient): Array<{ method: string; args: unknown[] }> {
  return (client as unknown as { __calls: Array<{ method: string; args: unknown[] }> }).__calls;
}

describe("tool surface", () => {
  it("exposes exactly the v1 tool set", () => {
    expect(TOOL_DEFINITIONS.map((tool) => tool.name).sort()).toEqual(
      [
        "add_comment",
        "assign_issue",
        "cancel_run",

describe("run lifecycle tools (RUYI-292)", () => {
  const run = {
    id: "t1",
    status: "running",
    agent_id: "a1",
    created_at: "2026-09-30T10:00:00Z",
  };

  it("list_issue_runs passes status/trigger/limit through and maps trigger buckets", async () => {
    const client = fakeClient({
      listIssueRuns: async (_ws: string, issue: string, params: Record<string, unknown>) => {
        callsOf(client).push({ method: "listIssueRuns", args: [issue, params] });
        return {
          tasks: [
            { ...run },
            { ...run, id: "t2", status: "queued", rerun_of_task_id: "t0" },
          ],
        };
      },
    });
    const tool = findTool("list_issue_runs");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", status: "running,pending", trigger: "rerun", limit: 50 },
      client,
    )) as { total: number; runs: Array<{ trigger: string }> };
    expect(callsOf(client)[0]).toEqual({
      method: "listIssueRuns",
      args: ["VOI-1", { status: "running,pending", trigger: "rerun", limit: 50 }],
    });
    expect(result.total).toBe(2);
    expect(result.runs[0]?.trigger).toBe("other");
    expect(result.runs[1]?.trigger).toBe("rerun");
  });

  it("get_run returns chain detail", async () => {
    const client = fakeClient({
      getIssueRun: async () => ({
        task: { ...run, error: "boom", failure_reason: "agent_error" },
        ancestors: [{ id: "t0", agent_id: "a1", status: "failed", attempt: 1 }],
        descendants: [],
      }),
    });
    const result = (await findTool("get_run")?.handler(
      { workspace: WS, issue: "VOI-1", run_id: "t1" },
      client,
    )) as { failure_reason: string; ancestors: unknown[] };
    expect(result.failure_reason).toBe("agent_error");
    expect(result.ancestors).toHaveLength(1);
  });

  it("cancel_run reports the two-phase accept and the 409 conflict dialect", async () => {
    const pending = fakeClient({
      cancelIssueRun: async () => ({ code: "cancel_requested", task: { ...run, status: "cancel_requested" } }),
    });
    const ok = (await findTool("cancel_run")?.handler(
      { workspace: WS, issue: "VOI-1", run_id: "t1" },
      pending,
    )) as { code: string; cancelled: boolean; stop_pending: boolean };
    expect(ok.code).toBe("cancel_requested");
    expect(ok.cancelled).toBe(false);
    expect(ok.stop_pending).toBe(true);

    const conflict = fakeClient({
      cancelIssueRun: async () => {
        throw new MulticaApiError(409, "not_cancellable: run already finished");
      },
    });
    const done = (await findTool("cancel_run")?.handler(
      { workspace: WS, issue: "VOI-1", run_id: "t1" },
      conflict,
    )) as { code: string; cancelled: boolean };
    expect(done.code).toBe("not_cancellable");
    expect(done.cancelled).toBe(false);
  });

  it("cancel_run propagates 403/404 errors", async () => {
    for (const status of [403, 404]) {
      const client = fakeClient({
        cancelIssueRun: async () => {
          throw new MulticaApiError(status, "denied");
        },
      });
      const err = await findTool("cancel_run")
        ?.handler({ workspace: WS, issue: "VOI-1", run_id: "t1" }, client)
        .catch((e: unknown) => e);
      expect(err).toBeInstanceOf(MulticaApiError);
      expect((err as MulticaApiError).status).toBe(status);
    }
  });

  it("retry_run returns the new run linkage", async () => {
    const client = fakeClient({
      retryIssueRun: async () => ({ ...run, id: "t9", status: "queued", rerun_of_task_id: "t1" }),
    });
    const result = (await findTool("retry_run")?.handler(
      { workspace: WS, issue: "VOI-1", run_id: "t1" },
      client,
    )) as { new_run_id: string; rerun_of_task_id: string };
    expect(result.new_run_id).toBe("t9");
    expect(result.rerun_of_task_id).toBe("t1");
  });

  it("retry_run propagates the 409 anti-storm codes", async () => {
    const client = fakeClient({
      retryIssueRun: async () => {
        throw new MulticaApiError(409, "agent_already_queued: the agent already has an unfinished run on this issue");
      },
    });
    const err = await findTool("retry_run")
      ?.handler({ workspace: WS, issue: "VOI-1", run_id: "t1" }, client)
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(MulticaApiError);
    expect((err as MulticaApiError).message).toContain("agent_already_queued");
  });
});
