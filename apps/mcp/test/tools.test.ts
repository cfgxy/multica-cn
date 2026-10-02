import { describe, expect, it } from "vitest";

import { MulticaApiError, MulticaClient } from "../src/rest.js";
import { findTool, TOOL_DEFINITIONS } from "../src/tools.js";
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
        "create_issue",
        "dispatch_agent",
        "get_issue",
        "get_run",
        "list_agents",
        "list_issue_runs",
        "list_issues",
        "list_projects",
        "list_workspaces",
        "progress_digest",
        "retry_run",
        "search_issues",
        "update_issue",
        "update_issue_status",
      ].sort(),
    );
  });

  it("documents the quota cost on dispatch and comment tools", () => {
    for (const name of ["dispatch_agent", "add_comment", "update_issue_status", "assign_issue"]) {
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

describe("update_issue handler (RUYI-350)", () => {
  it("title-only PATCH sends only the title", async () => {
    const client = fakeClient();
    const tool = findTool("update_issue");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", title: "Rewritten title" },
      client,
    )) as Record<string, unknown>;
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(body).toEqual({ title: "Rewritten title" });
    expect(result.updated).toBe(true);
    expect(result.revision).toBe(3);
  });

  it("description-only PATCH sends only the description, Markdown intact", async () => {
    const client = fakeClient();
    const markdown = "# Rewritten body\n\n- item **one**\n- item two\n\n```go\nfmt.Println(\"hi\")\n```";
    const tool = findTool("update_issue");
    await tool?.handler({ workspace: WS, issue: "VOI-1", description: markdown }, client);
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(body).toEqual({ description: markdown });
  });

  it("maps every editable field on a multi-field PATCH", async () => {
    const client = fakeClient();
    const tool = findTool("update_issue");
    await tool?.handler(
      {
        workspace: WS,
        issue: "VOI-1",
        title: "Multi",
        description: "body",
        priority: "high",
        project_id: "p2",
        parent_issue_id: "11111111-2222-3333-4444-555555555555",
        start_date: "2026-10-03",
        due_date: "2026-10-09",
        expected_revision: 4,
      },
      client,
    );
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(body).toEqual({
      title: "Multi",
      description: "body",
      priority: "high",
      project_id: "p2",
      parent_issue_id: "11111111-2222-3333-4444-555555555555",
      start_date: "2026-10-03",
      due_date: "2026-10-09",
      expected_revision: 4,
    });
  });

  it("clears nullable fields with an explicit null and keeps omitted fields absent", async () => {
    const client = fakeClient();
    const tool = findTool("update_issue");
    await tool?.handler(
      { workspace: WS, issue: "VOI-1", due_date: null, parent_issue_id: null, title: "keep-others" },
      client,
    );
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(body).toEqual({ title: "keep-others", due_date: null, parent_issue_id: null });
  });

  it("treats an empty string as a clear for nullable fields", async () => {
    const client = fakeClient();
    const tool = findTool("update_issue");
    await tool?.handler(
      { workspace: WS, issue: "VOI-1", start_date: "", project_id: "" },
      client,
    );
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(body).toEqual({ start_date: null, project_id: null });
  });

  it("rejects an empty PATCH (nothing to update)", async () => {
    const tool = findTool("update_issue");
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1" }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
  });

  it("rejects malformed dates and oversized text client-side", async () => {
    const tool = findTool("update_issue");
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1", start_date: "10/03/2026" }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1", title: "x".repeat(501) }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1", description: "x".repeat(50_001) }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1", priority: "asap" }, fakeClient()),
    ).rejects.toThrow(ToolInputError);
  });

  it("answers a stale expected_revision with a structured revision_conflict, no write", async () => {
    const client = fakeClient({
      updateIssue: async () => {
        throw new MulticaApiError(409, "revision_conflict: resource changed since it was loaded");
      },
    });
    const tool = findTool("update_issue");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", title: "stale write", expected_revision: 2 },
      client,
    )) as Record<string, unknown>;
    expect(result.updated).toBe(false);
    expect(result.code).toBe("revision_conflict");
    expect(result.hint).toMatch(/get_issue/);
  });

  it("propagates non-conflict server errors (403) intact", async () => {
    const client = fakeClient({
      updateIssue: async () => {
        throw new MulticaApiError(403, "you do not have access to this workspace");
      },
    });
    const tool = findTool("update_issue");
    const err = await tool
      ?.handler({ workspace: WS, issue: "VOI-1", title: "nope" }, client)
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(MulticaApiError);
    expect((err as MulticaApiError).status).toBe(403);
  });
});

describe("update_issue schema and serialization contract (RUYI-350)", () => {
  it("documents the no-run guarantee, optimistic locking and clear semantics", () => {
    const tool = findTool("update_issue");
    expect(tool?.description).toMatch(/never triggers an agent run/i);
    expect(tool?.description).toMatch(/no quota/i);
    expect(tool?.description).toMatch(/revision_conflict/i);
    expect(tool?.description).toMatch(/null/);
    expect(tool?.description).toMatch(/assign_issue/);
    expect(tool?.description).toMatch(/identifier|UUID/i);
    const schema = tool?.inputSchema as Record<string, unknown>;
    expect(schema.required).toEqual(["workspace", "issue"]);
    const props = schema.properties as Record<
      string,
      { description: string; type: string | string[]; minimum?: number }
    >;
    for (const name of ["project_id", "parent_issue_id", "start_date", "due_date"]) {
      expect(props[name]?.type).toContain("null");
    }
    expect(props.expected_revision?.description).toMatch(/revision_conflict/);
    expect(props.expected_revision?.minimum).toBe(1);
  });

  it("serializes explicit nulls into the wire body and drops omitted fields (PATCH contract guard)", async () => {
    // Runs the tool against the REAL MulticaClient over a mocked HTTP layer.
    // PATCH semantics live in the JSON wire format: a clear must survive as an
    // explicit null (the server decides by rawFields key presence) and an
    // omitted field must stay absent. JSON.stringify drops undefined keys and
    // keeps nulls — this pins that contract end-to-end.
    let wireBody = "";
    const fetchImpl = (async (_input: RequestInfo | URL, init?: RequestInit) => {
      wireBody = String(init?.body ?? "");
      return new Response(JSON.stringify(issueFixture({ revision: 9 })), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;
    const client = new MulticaClient({
      serverUrl: "https://api.example.com",
      token: "mul_test",
      fetchImpl,
    });
    const tool = findTool("update_issue");
    await tool?.handler(
      {
        workspace: WS,
        issue: "VOI-1",
        title: "wire",
        due_date: null,
        priority: "low",
      },
      client,
    );
    const parsed = JSON.parse(wireBody) as Record<string, unknown>;
    expect(Object.keys(parsed).sort()).toEqual(["due_date", "priority", "title"]);
    expect(parsed.due_date).toBeNull();
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
