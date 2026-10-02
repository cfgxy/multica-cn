import { describe, expect, it } from "vitest";

import { MulticaApiError, MulticaClient, MulticaRequestError } from "../src/rest.js";
import { findTool, TOOL_DEFINITIONS } from "../src/tools.js";
import { ToolInputError } from "../src/schemas.js";
import type { IssueInfo, ProjectInfo } from "../src/types.js";

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
    getProject: async (_ws: string, id: string) => {
      calls.push({ method: "getProject", args: [id] });
      return projectFixture();
    },
    createProject: async (_ws: string, body: Record<string, unknown>) => {
      calls.push({ method: "createProject", args: [body] });
      return { ...projectFixture(), ...body, revision: 1 };
    },
    updateProject: async (_ws: string, id: string, body: Record<string, unknown>) => {
      calls.push({ method: "updateProject", args: [id, body] });
      return { ...projectFixture(), title: "Playground v2", revision: 2 };
    },
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

function projectFixture(over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    id: "p1",
    workspace_id: "w1",
    title: "Playground",
    description: "sandbox",
    instructions: "brief text",
    status: "in_progress",
    priority: "high",
    created_at: "2026-10-03T00:00:00Z",
    updated_at: "2026-10-03T00:00:00Z",
    issue_count: 3,
    done_count: 1,
    resource_count: 2,
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
        "bulk_update_issues",
        "cancel_run",
        "create_issue",
        "create_project",
        "dispatch_agent",
        "get_issue",
        "get_project",
        "get_run",
        "list_agents",
        "list_issue_runs",
        "list_issues",
        "list_projects",
        "list_workspaces",
        "progress_digest",
        "retry_run",
        "search_issues",
        "update_issue_status",
        "update_project",
      ].sort(),
    );
  });

  it("documents the quota cost on dispatch and comment tools", () => {
    for (const name of ["dispatch_agent", "add_comment", "update_issue_status", "assign_issue", "bulk_update_issues"]) {
      const tool = findTool(name);
      expect(tool?.description).toMatch(/quota|run/i);
    }
  });

  it("every tool schema requires workspace where expected", () => {
    expect(findTool("list_workspaces")?.inputSchema.required).toEqual([]);
    for (const name of TOOL_DEFINITIONS.map((t) => t.name).filter((n) => n !== "list_workspaces")) {
      expect(findTool(name)?.inputSchema.required).toContain("workspace");
    }
  });
});

describe("list_projects handler", () => {
  it("returns brief project refs", async () => {
    const tool = findTool("list_projects");
    const result = (await tool?.handler({ workspace: WS }, fakeClient())) as Record<
      string,
      unknown
    >;
    expect(result.total).toBe(1);
    expect((result.projects as Array<{ title: string }>)[0]?.title).toBe("Playground");
  });
});

describe("get_project handler", () => {
  it("returns the full base-field projection including revision", async () => {
    const client = fakeClient();
    const tool = findTool("get_project");
    const result = (await tool?.handler(
      { workspace: WS, project_id: "p1" },
      client,
    )) as Record<string, unknown>;
    const [id] = callsOf(client)[0]?.args as [string];
    expect(id).toBe("p1");
    expect(result.id).toBe("p1");
    expect(result.title).toBe("Playground");
    expect(result.instructions).toBe("brief text");
    expect(result.done_count).toBe(1);
    expect(result.resource_count).toBe(2);
    expect(result.revision).toBe(1);
  });

  it("requires the project id", async () => {
    const tool = findTool("get_project");
    await expect(tool?.handler({ workspace: WS }, fakeClient())).rejects.toThrow(ToolInputError);
  });
});

describe("create_project handler", () => {
  it("maps creation fields to the REST body and echoes revision", async () => {
    const client = fakeClient();
    const tool = findTool("create_project");
    const result = (await tool?.handler(
      {
        workspace: WS,
        title: "  New Project  ",
        description: "desc",
        instructions: "brief",
        status: "in_progress",
        priority: "high",
        lead_type: "member",
        lead_id: "u1",
        start_date: "2026-10-03",
        due_date: "2026-10-31",
      },
      client,
    )) as Record<string, unknown>;
    const [body] = callsOf(client)[0]?.args as [Record<string, unknown>];
    expect(body.title).toBe("New Project");
    expect(body.status).toBe("in_progress");
    expect(body.lead_type).toBe("member");
    expect(body.start_date).toBe("2026-10-03");
    expect(result.created).toBe(true);
    expect(result.id).toBe("p1");
    expect(result.revision).toBe(1);
  });

  it("rejects lead_type without lead_id", async () => {
    const tool = findTool("create_project");
    await expect(
      tool?.handler({ workspace: WS, title: "t", lead_type: "agent" }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
  });

  it("rejects an unknown status and a malformed date before hitting the API", async () => {
    const tool = findTool("create_project");
    await expect(
      tool?.handler({ workspace: WS, title: "t", status: "active" }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
    await expect(
      tool?.handler({ workspace: WS, title: "t", due_date: "31/10/2026" }, fakeClient()),
    ).rejects.toThrow(/YYYY-MM-DD/);
  });

  it("rejects oversized instructions before hitting the API", async () => {
    const tool = findTool("create_project");
    await expect(
      tool?.handler({ workspace: WS, title: "t", instructions: "x".repeat(32_001) }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
  });
});

describe("update_project handler", () => {
  it("maps set fields, passes expected_revision through, and echoes the new revision", async () => {
    const client = fakeClient();
    const tool = findTool("update_project");
    const result = (await tool?.handler(
      {
        workspace: WS,
        project_id: "p1",
        title: "Renamed",
        status: "paused",
        expected_revision: 1,
      },
      client,
    )) as Record<string, unknown>;
    const [id, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(id).toBe("p1");
    expect(body).toEqual({ title: "Renamed", status: "paused", expected_revision: 1 });
    expect(result.updated).toBe(true);
    expect(result.id).toBe("p1");
    expect(result.revision).toBe(2);
  });

  it("omits absent keys and sends explicit nulls for clears", async () => {
    const client = fakeClient();
    const tool = findTool("update_project");
    await tool?.handler(
      {
        workspace: WS,
        project_id: "p1",
        description: null,
        instructions: null,
        lead_type: null,
        lead_id: null,
        start_date: null,
        due_date: "",
      },
      client,
    );
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    // JSON round-trip is what actually goes on the wire: undefined keys must
    // vanish, null keys must survive.
    const wire = JSON.parse(JSON.stringify(body)) as Record<string, unknown>;
    expect(wire).toEqual({
      description: null,
      instructions: null,
      lead_type: null,
      lead_id: null,
      start_date: null,
      due_date: null,
    });
  });

  it("keeps date values that are set and rejects malformed ones", async () => {
    const client = fakeClient();
    const tool = findTool("update_project");
    await tool?.handler(
      { workspace: WS, project_id: "p1", due_date: "2026-11-30" },
      client,
    );
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(body.due_date).toBe("2026-11-30");

    await expect(
      tool?.handler({ workspace: WS, project_id: "p1", due_date: "tomorrow" }, fakeClient()),
    ).rejects.toThrow(/YYYY-MM-DD/);
  });

  it("requires lead_type and lead_id together", async () => {
    const tool = findTool("update_project");
    await expect(
      tool?.handler({ workspace: WS, project_id: "p1", lead_type: "agent" }, fakeClient()),
    ).rejects.toThrow(/together/);
    await expect(
      tool?.handler({ workspace: WS, project_id: "p1", lead_id: "a1" }, fakeClient()),
    ).rejects.toThrow(/together/);
    await expect(
      tool?.handler(
        { workspace: WS, project_id: "p1", lead_type: null, lead_id: null },
        fakeClient(),
      ),
    ).resolves.toBeDefined();
  });

  it("rejects an unknown status enum", async () => {
    const tool = findTool("update_project");
    await expect(
      tool?.handler({ workspace: WS, project_id: "p1", status: "active" }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
  });
});

describe("create_issue handler", () => {
  it("maps general creation fields to the REST body", async () => {
    const client = fakeClient();
    const tool = findTool("create_issue");
    const result = (await tool?.handler(
      {
        workspace: WS,
        title: "  Voice idea  ",
        description: "capture",
        project_id: "p1",
        priority: "high",
        due_date: "2026-09-30",
      },
      client,
    )) as Record<string, unknown>;
    const [ws, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(ws).toBe(WS);
    expect(body.title).toBe("Voice idea");
    expect(body.priority).toBe("high");
    expect(body.project_id).toBe("p1");
    expect(result.created).toBe(true);
    expect(result.identifier).toBe("VOI-1");
  });

  it("rejects assignee_type without assignee_id", async () => {
    const tool = findTool("create_issue");
    await expect(
      tool?.handler({ workspace: WS, title: "t", assignee_type: "agent" }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
  });

  it("rejects malformed dates before hitting the API", async () => {
    const tool = findTool("create_issue");
    await expect(
      tool?.handler({ workspace: WS, title: "t", due_date: "30/09/2026" }, fakeClient()),
    ).rejects.toThrow(/YYYY-MM-DD/);
  });
});

describe("update_issue_status handler", () => {
  it("passes status and suppress_run through", async () => {
    const client = fakeClient();
    const tool = findTool("update_issue_status");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", status: "in_progress", suppress_run: true },
      client,
    )) as Record<string, unknown>;
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(body).toEqual({ status: "in_progress", suppress_run: true });
    expect(result.updated).toBe(true);
    expect(result.status).toBe("in_progress");
  });
});

describe("dispatch_agent handler", () => {
  it("routes through quick-create and returns the task id", async () => {
    const client = fakeClient();
    const tool = findTool("dispatch_agent");
    const result = (await tool?.handler(
      {
        workspace: WS,
        agent_id: "a1",
        prompt: "顾小鱼 please summarize RUYI-82 progress",
        priority: "medium",
      },
      client,
    )) as Record<string, unknown>;
    const [ws, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(ws).toBe(WS);
    expect(body.agent_id).toBe("a1");
    expect(body.prompt).toContain("RUYI-82");
    expect(result.dispatched).toBe(true);
    expect(result.task_id).toBe("task-1");
  });

  it("rejects an empty prompt", async () => {
    const tool = findTool("dispatch_agent");
    await expect(
      tool?.handler({ workspace: WS, agent_id: "a1", prompt: "  " }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
  });
});

describe("get_issue handler", () => {
  it("includes comments by default", async () => {
    const client = fakeClient({
      listComments: async () => [{ id: "c1", content: "hello" }],
    });
    const tool = findTool("get_issue");
    const result = (await tool?.handler({ workspace: WS, issue: "VOI-1" }, client)) as Record<
      string,
      unknown
    >;
    expect((result.issue as IssueInfo).identifier).toBe("VOI-1");
    expect(result.comments).toHaveLength(1);
  });

  it("skips comments when include_comments is false", async () => {
    const client = fakeClient({
      listComments: async () => {
        throw new Error("should not be called");
      },
    });
    const tool = findTool("get_issue");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", include_comments: false },
      client,
    )) as Record<string, unknown>;
    expect(result.comments).toBeUndefined();
  });
});

describe("progress_digest handler", () => {
  it("probes per-status totals and builds the digest", async () => {
    const queried: Array<Record<string, unknown>> = [];
    const client = fakeClient({
      listIssues: async (_ws: string, params: Record<string, unknown>) => {
        queried.push(params);
        if (params.status === "todo") {
          return { issues: [], total: 7 };
        }
        return { issues: [], total: 0 };
      },
    });
    const tool = findTool("progress_digest");
    const result = (await tool?.handler({ workspace: WS }, client)) as Record<string, unknown>;
    const statusProbes = queried.filter((params) => "status" in params);
    expect(statusProbes).toHaveLength(5);
    expect(result.counts_by_status).toEqual({
      backlog: 0,
      todo: 7,
      in_progress: 0,
      in_review: 0,
      blocked: 0,
    });
    expect(result.open_total).toBe(7);
  });

  it("propagates project_id to every probe", async () => {
    const queried: Array<Record<string, unknown>> = [];
    const client = fakeClient({
      listIssues: async (_ws: string, params: Record<string, unknown>) => {
        queried.push(params);
        return { issues: [], total: 0 };
      },
    });
    const tool = findTool("progress_digest");
    await tool?.handler({ workspace: WS, project_id: "p9" }, client);
    for (const params of queried) {
      expect(params.project_id).toBe("p9");
    }
  });
});

describe("assign_issue handler", () => {
  function updateIssueOf(
    client: MulticaClient,
  ): Array<{ id: string; body: Record<string, unknown> }> {
    return callsOf(client)
      .filter((call) => call.method === "updateIssue")
      .map((call) => ({
        id: call.args[0] as string,
        body: call.args[1] as Record<string, unknown>,
      }));
  }

  it("assigns to each assignee kind (member, agent, squad)", async () => {
    const tool = findTool("assign_issue");
    for (const assigneeType of ["member", "agent", "squad"] as const) {
      const client = fakeClient();
      await tool?.handler(
        { workspace: WS, issue: "VOI-1", assignee_type: assigneeType, assignee_id: "u1" },
        client,
      );
      const [call] = updateIssueOf(client);
      expect(call?.body).toEqual({ assignee_type: assigneeType, assignee_id: "u1" });
    }
  });

  it("reassigns from A to B with a plain pair body", async () => {
    const captured: Array<Record<string, unknown>> = [];
    const client = fakeClient({
      updateIssue: async (_ws: string, _id: string, body: Record<string, unknown>) => {
        captured.push(body);
        return issueFixture({
          status: "in_progress",
          revision: 3,
          assignee_type: "agent",
          assignee_id: "agent-b",
        });
      },
    });
    const tool = findTool("assign_issue");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", assignee_type: "agent", assignee_id: "agent-b" },
      client,
    )) as Record<string, unknown>;
    expect(captured[0]).toEqual({ assignee_type: "agent", assignee_id: "agent-b" });
    expect(JSON.stringify(captured[0])).not.toContain("null");
    expect(result.assigned).toBe(true);
    expect(result.assignee_type).toBe("agent");
    expect(result.assignee_id).toBe("agent-b");
  });

  it("unassigns via the literal 'unassigned' and sends explicit JSON nulls", async () => {
    const client = fakeClient();
    const tool = findTool("assign_issue");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", assignee_type: "unassigned" },
      client,
    )) as Record<string, unknown>;
    const [call] = updateIssueOf(client);
    expect(call).toBeDefined();
    const body = call?.body ?? {};
    expect(Object.hasOwn(body, "assignee_type")).toBe(true);
    expect(Object.hasOwn(body, "assignee_id")).toBe(true);
    expect(body.assignee_type).toBeNull();
    expect(body.assignee_id).toBeNull();
    const wire = JSON.stringify(body);
    expect(wire).toContain('"assignee_type":null');
    expect(wire).toContain('"assignee_id":null');
    expect(wire).not.toContain('""');
    expect(result.assigned).toBe(false);
  });

  it("accepts identifier and UUID issue references verbatim", async () => {
    const tool = findTool("assign_issue");
    const uuid = "0b7f4c1e-1111-4222-8333-abcdefabcdef";
    for (const issue of ["VOI-1", uuid]) {
      const client = fakeClient();
      await tool?.handler(
        { workspace: WS, issue, assignee_type: "member", assignee_id: "u1" },
        client,
      );
      const [call] = updateIssueOf(client);
      expect(call?.id).toBe(issue);
    }
  });

  it("passes suppress_run, handoff_note and expected_revision through", async () => {
    const client = fakeClient();
    const tool = findTool("assign_issue");
    await tool?.handler(
      {
        workspace: WS,
        issue: "VOI-1",
        assignee_type: "squad",
        assignee_id: "s1",
        suppress_run: true,
        handoff_note: "先跑回归再动实现",
        expected_revision: 4,
      },
      client,
    );
    const [call] = updateIssueOf(client);
    expect(call?.body).toEqual({
      assignee_type: "squad",
      assignee_id: "s1",
      suppress_run: true,
      handoff_note: "先跑回归再动实现",
      expected_revision: 4,
    });
  });

  it("rejects an unknown assignee_type before hitting the API", async () => {
    const tool = findTool("assign_issue");
    await expect(
      tool?.handler(
        { workspace: WS, issue: "VOI-1", assignee_type: "robot", assignee_id: "r1" },
        fakeClient(),
      ),
    ).rejects.toThrow(ToolInputError);
  });

  it("rejects a missing assignee_id for a concrete assignee_type", async () => {
    const tool = findTool("assign_issue");
    for (const assigneeType of ["member", "agent", "squad"]) {
      await expect(
        tool?.handler({ workspace: WS, issue: "VOI-1", assignee_type: assigneeType }, fakeClient()),
      ).rejects.toThrow(ToolInputError);
    }
  });

  it("rejects assignee_id combined with 'unassigned'", async () => {
    const tool = findTool("assign_issue");
    await expect(
      tool?.handler(
        { workspace: WS, issue: "VOI-1", assignee_type: "unassigned", assignee_id: "u1" },
        fakeClient(),
      ),
    ).rejects.toThrow(ToolInputError);
  });

  it("propagates server errors with status and message intact", async () => {
    const tool = findTool("assign_issue");
    const conflict = fakeClient({
      updateIssue: async () => {
        throw new MulticaApiError(409, "issue_revision_conflict: issue changed since revision 3");
      },
    });
    const err = await tool
      ?.handler(
        { workspace: WS, issue: "VOI-1", assignee_type: "agent", assignee_id: "a9" },
        conflict,
      )
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(MulticaApiError);
    expect((err as MulticaApiError).status).toBe(409);
    expect((err as MulticaApiError).message).toContain("issue changed since revision 3");

    const forbidden = fakeClient({
      updateIssue: async () => {
        throw new MulticaApiError(403, "you do not have permission to assign work to this agent");
      },
    });
    const err2 = await tool
      ?.handler(
        { workspace: WS, issue: "VOI-1", assignee_type: "agent", assignee_id: "a9" },
        forbidden,
      )
      .catch((e: unknown) => e);
    expect(err2).toBeInstanceOf(MulticaApiError);
    expect((err2 as MulticaApiError).status).toBe(403);
  });

  it("surfaces the server's run_suppressed snapshot in the result", async () => {
    const client = fakeClient({
      updateIssue: async () => issueFixture({ revision: 7, run_suppressed: true }),
    });
    const tool = findTool("assign_issue");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", assignee_type: "agent", assignee_id: "a1", suppress_run: true },
      client,
    )) as Record<string, unknown>;
    expect(result.run_suppressed).toBe(true);
    expect(result.revision).toBe(7);
  });
});

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
        // The server answers task-runs with a bare array; keep this mock
        // shaped like the real payload, not like a client-side wrapper.
        return [
          { ...run },
          { ...run, id: "t2", status: "queued", rerun_of_task_id: "t0" },
        ];
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

  it("list_issue_runs consumes the server's bare-array task-runs payload (contract drift guard)", async () => {
    // Runs the tool against the REAL MulticaClient over a mocked HTTP layer:
    // GET task-runs answers a bare array (writeJSON of []AgentTaskResponse).
    // The first QA pass of RUYI-292 crashed real stdio calls because the
    // client declared a wrapper-object shape and the tool tests mocked the
    // same wrong shape — unit-green, integration-dead. This fails again if
    // either side drifts from the bare-array contract.
    const fetchImpl = (async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(
        JSON.stringify([run, { ...run, id: "t2", status: "queued", rerun_of_task_id: "t0" }]),
        { status: 200, headers: { "Content-Type": "application/json" } },
      )) as typeof fetch;
    const client = new MulticaClient({
      serverUrl: "https://api.example.com",
      token: "mul_test",
      fetchImpl,
    });
    const tool = findTool("list_issue_runs");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", limit: 50 },
      client,
    )) as { total: number };
    expect(result.total).toBe(2);
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

describe("bulk_update_issues (RUYI-353)", () => {
  function updateIssueCalls(
    client: MulticaClient,
  ): Array<{ id: string; body: Record<string, unknown> }> {
    return callsOf(client)
      .filter((call) => call.method === "updateIssue")
      .map((call) => ({ id: call.args[0] as string, body: call.args[1] as Record<string, unknown> }));
  }

  it("applies each item through the single-issue write path and reports index-aligned per-item results", async () => {
    const client = fakeClient({
      updateIssue: async (_ws: string, id: string, body: Record<string, unknown>) => {
        callsOf(client).push({ method: "updateIssue", args: [id, body] });
        return issueFixture({ id, identifier: id, revision: 5 });
      },
    });
    const tool = findTool("bulk_update_issues");
    const result = (await tool?.handler(
      {
        workspace: WS,
        updates: [
          { issue: "VOI-1", status: "done" },
          { issue: "VOI-2", priority: "high", project_id: "p1" },
        ],
      },
      client,
    )) as Record<string, unknown>;
    const calls = updateIssueCalls(client);
    expect(calls).toHaveLength(2);
    expect(calls[0]).toEqual({ id: "VOI-1", body: { status: "done" } });
    expect(calls[1]).toEqual({ id: "VOI-2", body: { priority: "high", project_id: "p1" } });
    expect(result.total).toBe(2);
    expect(result.updated).toBe(2);
    expect(result.failed).toBe(0);
    expect(result.skipped).toBe(0);
    const results = result.results as Array<Record<string, unknown>>;
    expect(results).toHaveLength(2);
    expect(results[0]).toMatchObject({
      index: 0,
      issue: "VOI-1",
      outcome: "updated",
      id: "VOI-1",
      revision: 5,
    });
    expect(results[1]).toMatchObject({ index: 1, issue: "VOI-2", outcome: "updated" });
  });

  it("mixed batch: success, conflict and forbidden each get their own result — nothing silently skipped", async () => {
    const client = fakeClient({
      updateIssue: async (_ws: string, id: string, body: Record<string, unknown>) => {
        callsOf(client).push({ method: "updateIssue", args: [id, body] });
        if (id === "VOI-STALE") {
          throw new MulticaApiError(409, "revision_conflict: resource changed since it was loaded");
        }
        if (id === "VOI-DENIED") {
          throw new MulticaApiError(403, "you do not have permission to assign work to this agent");
        }
        if (id === "VOI-GONE") {
          throw new MulticaApiError(404, "issue not found in this workspace");
        }
        return issueFixture({ id, identifier: id, revision: 6 });
      },
    });
    const tool = findTool("bulk_update_issues");
    const result = (await tool?.handler(
      {
        workspace: WS,
        updates: [
          { issue: "VOI-OK", status: "done" },
          { issue: "VOI-STALE", status: "done", expected_revision: 4 },
          { issue: "VOI-DENIED", assignee_type: "agent", assignee_id: "a9" },
          { issue: "VOI-GONE", status: "blocked" },
        ],
      },
      client,
    )) as Record<string, unknown>;
    expect(updateIssueCalls(client)).toHaveLength(4); // continue mode attempts every item
    expect(result.updated).toBe(1);
    expect(result.failed).toBe(3);
    expect(result.skipped).toBe(0);
    const results = result.results as Array<Record<string, unknown>>;
    expect(results.map((item) => item.issue)).toEqual([
      "VOI-OK",
      "VOI-STALE",
      "VOI-DENIED",
      "VOI-GONE",
    ]);
    expect(results[1]?.outcome).toBe("failed");
    expect(results[1]?.error).toMatchObject({ code: "conflict" });
    expect((results[1]?.error as { message: string }).message).toContain("revision_conflict");
    expect(results[2]?.error).toMatchObject({ code: "forbidden" });
    expect((results[2]?.error as { message: string }).message).toContain(
      "you do not have permission to assign work to this agent",
    );
    expect(results[3]?.error).toMatchObject({ code: "not_found" });
    // A failed item carries only its own identifier and failure class — no
    // other item's content leaks through it.
    expect(JSON.stringify(results[2])).not.toContain("VOI-OK");
  });

  it("on_error='stop' stops at the first failure and marks the rest not_attempted", async () => {
    const client = fakeClient({
      updateIssue: async (_ws: string, id: string, body: Record<string, unknown>) => {
        callsOf(client).push({ method: "updateIssue", args: [id, body] });
        if (id === "VOI-2") {
          throw new MulticaApiError(409, "revision_conflict: resource changed since it was loaded");
        }
        return issueFixture({ id, identifier: id, revision: 9 });
      },
    });
    const tool = findTool("bulk_update_issues");
    const result = (await tool?.handler(
      {
        workspace: WS,
        on_error: "stop",
        updates: [
          { issue: "VOI-1", status: "done" },
          { issue: "VOI-2", status: "done" },
          { issue: "VOI-3", status: "done" },
        ],
      },
      client,
    )) as Record<string, unknown>;
    expect(updateIssueCalls(client)).toHaveLength(2); // VOI-3 was never attempted
    expect(result.updated).toBe(1);
    expect(result.failed).toBe(1);
    expect(result.skipped).toBe(1);
    expect((result.results as Array<Record<string, unknown>>)[2]).toEqual({
      index: 2,
      issue: "VOI-3",
      outcome: "skipped",
      reason: "not_attempted",
    });
  });

  it("rejects a batch over the 50-item limit wholesale before any write", async () => {
    const client = fakeClient();
    const tool = findTool("bulk_update_issues");
    const updates = Array.from({ length: 51 }, (_, i) => ({ issue: `VOI-${i + 1}`, status: "done" }));
    await expect(tool?.handler({ workspace: WS, updates }, client)).rejects.toThrow(/50/);
    expect(updateIssueCalls(client)).toHaveLength(0);
  });

  it("rejects empty and non-array updates before any write", async () => {
    const tool = findTool("bulk_update_issues");
    for (const updates of [[], "VOI-1", undefined]) {
      const client = fakeClient();
      await expect(tool?.handler({ workspace: WS, updates }, client)).rejects.toThrow(ToolInputError);
      expect(updateIssueCalls(client)).toHaveLength(0);
    }
  });

  it("validates every item up front; any bad item means zero API calls", async () => {
    const badUpdates: Array<Record<string, unknown>> = [
      { status: "done" }, // missing issue
      { issue: "VOI-1" }, // no writable field
      { issue: "VOI-1", title: "rename", status: "done" }, // unsupported field would be a silent no-op
      { issue: "VOI-1", assignee_type: "member" }, // assignee_id missing
      { issue: "VOI-1", assignee_id: "u1" }, // assignee_type missing
      { issue: "VOI-1", assignee_type: "unassigned", assignee_id: "u1" },
      { issue: "VOI-1", due_date: "30/09/2026" },
      { issue: "VOI-1", priority: "asap" },
      { issue: "VOI-1", status: "done", expected_revision: 0 }, // server requires a positive revision
    ];
    const tool = findTool("bulk_update_issues");
    for (const bad of badUpdates) {
      const client = fakeClient();
      await expect(
        tool?.handler(
          { workspace: WS, updates: [bad, { issue: "VOI-2", status: "done" }] },
          client,
        ),
      ).rejects.toThrow(ToolInputError);
      expect(updateIssueCalls(client)).toHaveLength(0);
    }
    const client = fakeClient();
    await expect(
      tool?.handler(
        { workspace: WS, updates: [{ issue: "VOI-1", status: "done" }], on_error: "rewind" },
        client,
      ),
    ).rejects.toThrow(/on_error/);
    expect(updateIssueCalls(client)).toHaveLength(0);
  });

  it("suppress_run: batch-level default fills items, per-item value wins, unset omits the key", async () => {
    const tool = findTool("bulk_update_issues");
    const client = fakeClient();
    await tool?.handler(
      {
        workspace: WS,
        suppress_run: true,
        updates: [
          { issue: "VOI-1", status: "done" },
          { issue: "VOI-2", status: "done", suppress_run: false },
        ],
      },
      client,
    );
    const bodies = updateIssueCalls(client).map((call) => call.body);
    expect(bodies[0]).toEqual({ status: "done", suppress_run: true });
    expect(bodies[1]).toEqual({ status: "done", suppress_run: false });

    const unset = fakeClient();
    await tool?.handler({ workspace: WS, updates: [{ issue: "VOI-1", status: "done" }] }, unset);
    const body = updateIssueCalls(unset)[0]?.body ?? {};
    // Single-tool default: no suppress_run on the wire, runs trigger as usual.
    expect(Object.hasOwn(body, "suppress_run")).toBe(false);
  });

  it("per-item unassign sends the explicit JSON nulls the server contract requires", async () => {
    const client = fakeClient();
    const tool = findTool("bulk_update_issues");
    await tool?.handler(
      { workspace: WS, updates: [{ issue: "VOI-1", assignee_type: "unassigned" }] },
      client,
    );
    const body = updateIssueCalls(client)[0]?.body ?? {};
    expect(body.assignee_type).toBeNull();
    expect(body.assignee_id).toBeNull();
    const wire = JSON.stringify(body);
    expect(wire).toContain('"assignee_type":null');
    expect(wire).toContain('"assignee_id":null');
  });

  it("passes expected_revision and handoff_note through per item", async () => {
    const client = fakeClient();
    const tool = findTool("bulk_update_issues");
    await tool?.handler(
      {
        workspace: WS,
        updates: [
          {
            issue: "VOI-1",
            status: "in_review",
            expected_revision: 8,
            handoff_note: "先跑回归再收",
          },
        ],
      },
      client,
    );
    expect(updateIssueCalls(client)[0]?.body).toEqual({
      status: "in_review",
      expected_revision: 8,
      handoff_note: "先跑回归再收",
    });
  });

  it("classifies transport failures as transport_error and keeps attempting in continue mode", async () => {
    const client = fakeClient({
      updateIssue: async (_ws: string, id: string, body: Record<string, unknown>) => {
        callsOf(client).push({ method: "updateIssue", args: [id, body] });
        if (id === "VOI-TIMEOUT") {
          throw new MulticaRequestError("Multica API request PUT /api/issues/VOI-TIMEOUT timed out after 30000ms");
        }
        return issueFixture({ id, identifier: id, revision: 2 });
      },
    });
    const tool = findTool("bulk_update_issues");
    const result = (await tool?.handler(
      {
        workspace: WS,
        updates: [
          { issue: "VOI-1", status: "done" },
          { issue: "VOI-TIMEOUT", status: "done" },
          { issue: "VOI-2", status: "done" },
        ],
      },
      client,
    )) as Record<string, unknown>;
    expect(updateIssueCalls(client)).toHaveLength(3);
    const results = result.results as Array<Record<string, unknown>>;
    expect(results[1]?.outcome).toBe("failed");
    expect(results[1]?.error).toMatchObject({ code: "transport_error" });
    expect(results[2]).toMatchObject({ outcome: "updated" });
  });

  it("idempotent replay: re-sent items already applied fail as conflicts instead of writing twice", async () => {
    const revisions: Record<string, number> = { "VOI-1": 4, "VOI-2": 7 };
    let applied = 0;
    const apply = async (_ws: string, id: string, body: Record<string, unknown>) => {
      const current = revisions[id];
      if (current === undefined) {
        throw new MulticaApiError(404, "issue not found in this workspace");
      }
      if (body.expected_revision !== undefined && body.expected_revision !== current) {
        throw new MulticaApiError(
          409,
          `revision_conflict: expected ${body.expected_revision}, actual ${current}`,
        );
      }
      applied += 1;
      revisions[id] = current + 1;
      return issueFixture({ id, identifier: id, revision: current + 1 });
    };
    const updates = [
      { issue: "VOI-1", status: "done", expected_revision: 4 },
      { issue: "VOI-2", status: "cancelled", expected_revision: 7 },
    ];
    const tool = findTool("bulk_update_issues");
    const first = (await tool?.handler({ workspace: WS, updates }, fakeClient({ updateIssue: apply }))) as Record<string, unknown>;
    expect(first.updated).toBe(2);
    const second = (await tool?.handler({ workspace: WS, updates }, fakeClient({ updateIssue: apply }))) as Record<string, unknown>;
    expect(second.updated).toBe(0);
    expect(second.failed).toBe(2);
    for (const item of second.results as Array<{ error: { code: string } }>) {
      expect(item.error.code).toBe("conflict");
    }
    expect(applied).toBe(2); // the replay produced no additional write
  });
});
