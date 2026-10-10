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
    addIssueRelation: async (_ws: string, _id: string, body: Record<string, unknown>) => {
      calls.push({ method: "addIssueRelation", args: [body] });
      return {
        added: true,
        relation: { id: "e1", type: body.type, source_issue_id: "i1", target_issue_id: body.target_issue_id },
        issue: { id: "i1", revision: 4 },
      };
    },
    removeIssueRelation: async (
      _ws: string,
      _id: string,
      relationType: string,
      targetIssueId: string,
      expectedRevision?: number,
    ) => {
      calls.push({ method: "removeIssueRelation", args: [relationType, targetIssueId, expectedRevision] });
      return {
        removed: true,
        relation: { type: relationType, source_issue_id: "i1", target_issue_id: targetIssueId },
        issue: { id: "i1", revision: 5 },
      };
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
        "apply_execution_profile",
        "archive_agent",
        "archive_squad",
        "assign_issue",
        "bulk_update_agent_runtime_config",
        "bulk_update_issues",
        "cancel_run",
        "create_agent",
        "create_execution_profile",
        "create_issue",
        "create_project",
        "create_project_resource",
        "create_quick_reply",
        "create_squad",
        "delete_comment",
        "delete_execution_profile",
        "delete_project_resource",
        "delete_quick_reply",
        "dispatch_agent",
        "edit_comment",
        "get_agent",
        "get_agent_runtime_config",
        "get_comment",
        "get_daemon_instance",
        "get_execution_profile",
        "get_execution_topology",
        "get_issue",
        "get_issue_relations",
        "get_project",
        "get_run",
        "get_runtime",
        "get_runtime_models",
        "get_squad",
        "list_agents",
        "list_comments",
        "list_daemon_instances",
        "list_execution_profiles",
        "list_issue_runs",
        "list_issues",
        "list_projects",
        "list_project_resources",
        "list_quick_replies",
        "list_runs",
        "list_runtimes",
        "list_squads",
        "list_workspaces",
        "manage_issue_relations",
        "progress_digest",
        "restore_agent",
        "retry_run",
        "search_audit_events",
        "search_issues",
        "update_agent",
        "update_agent_runtime_config",
        "update_execution_profile",
        "update_issue",
        "update_issue_status",
        "update_project",
        "update_project_resource",
        "update_quick_reply",
        "update_squad",
      ].sort(),
    );
  });

  it("documents the quota cost on dispatch and comment tools", () => {
    for (const name of [
      "dispatch_agent",
      "add_comment",
      "edit_comment",
      "update_issue_status",
      "assign_issue",
      "bulk_update_issues",
    ]) {
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

  it("registers comment reads as read-only and comment writes as mutating", () => {
    for (const readTool of ["get_comment", "list_comments"]) {
      expect(findTool(readTool)?.description).toMatch(/read-only/i);
    }
    // The mutating comment tools declare their destructive/run side effects.
    expect(findTool("edit_comment")?.description).toMatch(/WARNING \(run side effects\)/);
    expect(findTool("delete_comment")?.description).toMatch(/SIDE EFFECTS/i);
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

describe("update_issue handler", () => {
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

describe("update_issue schema and serialization contract", () => {
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
        prompt: "顾小鱼 please summarize ENG-82 progress",
        priority: "medium",
      },
      client,
    )) as Record<string, unknown>;
    const [ws, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(ws).toBe(WS);
    expect(body.agent_id).toBe("a1");
    expect(body.prompt).toContain("ENG-82");
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

describe("run lifecycle tools", () => {
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
    // Guarding a real integration failure mode: if the client declares a
    // wrapper-object shape while the server answers a bare array, mocked
    // tool tests stay green while real stdio calls crash — unit-green,
    // integration-dead. This fails again if either side drifts from the
    // bare-array contract.
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

  it("search_audit_events passes filters through and surfaces the keyset cursor", async () => {
    const client = fakeClient({
      listAuditEvents: async (_ws: string, params: Record<string, unknown>) => {
        callsOf(client).push({ method: "listAuditEvents", args: [params] });
        return {
          events: [
            {
              id: "ev1",
              workspace_id: WS,
              domain: "run",
              event_type: "run.cancelled",
              occurred_at: "2026-10-04T00:00:00Z",
              actor_type: "member",
              actor_id: "u1",
              trigger_kind: null,
              trigger_ref: null,
              issue_id: "i1",
              task_id: "t1",
              agent_id: null,
              runtime_id: null,
              reason: "user_requested",
              details: {},
            },
          ],
          next_cursor: "2026-10-04T00:00:00Z",
          next_cursor_id: "ev1",
        };
      },
    });
    const tool = findTool("search_audit_events");
    const result = (await tool?.handler(
      { workspace: WS, domain: "run", reason: "user_requested", limit: 10 },
      client,
    )) as { total: number; events: unknown[]; next_cursor: string | null };
    expect(callsOf(client)[0]).toEqual({
      method: "listAuditEvents",
      args: [{ domain: "run", reason: "user_requested", limit: 10 }],
    });
    expect(result.total).toBe(1);
    expect(result.next_cursor).toBe("2026-10-04T00:00:00Z");
  });

  it("search_audit_events consumes the server's page wrapper over the real client (contract drift guard)", async () => {
    // Same guard rationale as list_issue_runs above: run the tool against the
    // REAL MulticaClient over a mocked HTTP layer so a client-declared shape
    // drifting from the server's { events, next_cursor, next_cursor_id }
    // wrapper cannot pass unit mocks while integration dies.
    const payload = {
      events: [
        {
          id: "ev1",
          workspace_id: WS,
          domain: "ops",
          event_type: "ops.server_started",
          occurred_at: "2026-10-04T00:00:00Z",
          actor_type: "system",
          actor_id: null,
          trigger_kind: null,
          trigger_ref: null,
          issue_id: null,
          task_id: null,
          agent_id: null,
          runtime_id: null,
          reason: null,
          details: { version: "dev", commit: "unknown" },
        },
      ],
      next_cursor: null,
      next_cursor_id: null,
    };
    // The client resolves a slug workspace to its UUID via the workspace
    // list before the audit call, so the stub answers per route like the
    // real server would.
    const wsUuid = "0b7f4c1e-1111-4222-8333-abcdefabcdef";
    const fetchImpl = (async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = input instanceof URL ? input : new URL(String(input));
      if (url.pathname === "/api/workspaces") {
        return new Response(
          JSON.stringify([{ id: wsUuid, name: "Voice Notes", slug: WS }]),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      return new Response(JSON.stringify(payload), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;
    const client = new MulticaClient({
      serverUrl: "https://api.example.com",
      token: "mul_test",
      fetchImpl,
    });
    const tool = findTool("search_audit_events");
    const result = (await tool?.handler({ workspace: WS }, client)) as {
      total: number;
      next_cursor: string | null;
    };
    expect(result.total).toBe(1);
    expect(result.next_cursor).toBeNull();
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

describe("issue relation tools", () => {
  it("get_issue_relations returns the structured five-view shape", async () => {
    const client = fakeClient({
      getIssueRelations: async () => ({
        issue_id: "i1",
        identifier: "VOI-1",
        revision: 3,
        parent: { id: "p1", identifier: "VOI-9" },
        blocks: [{ id: "b1", identifier: "VOI-2" }],
        blocked_by: [],
        relates_to: [{ id: "r1", identifier: "VOI-3" }],
        supersedes: [],
        superseded_by: [{ id: "s1", identifier: "VOI-4" }],
      }),
    });
    const tool = findTool("get_issue_relations");
    const result = (await tool?.handler({ workspace: WS, issue: "VOI-1" }, client)) as Record<
      string,
      unknown
    >;
    expect(result.issue_id).toBe("i1");
    expect(result.parent).toEqual({ id: "p1", identifier: "VOI-9" });
    expect(result.blocks).toHaveLength(1);
    expect(result.superseded_by).toHaveLength(1);
  });

  it("manage_issue_relations set_parent routes through updateIssue with the lock", async () => {
    const client = fakeClient();
    const tool = findTool("manage_issue_relations");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", action: "set_parent", target_issue: "VOI-9", expected_revision: 3 },
      client,
    )) as Record<string, unknown>;
    const [id, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(id).toBe("VOI-1");
    expect(body.parent_issue_id).toBe("VOI-9");
    expect(body.expected_revision).toBe(3);
    // The fixture issue has no parent_issue_id, so the echo falls back to null.
    expect(result.parent_issue_id).toBe(null);
    expect(String(result.note)).toMatch(/never/i);
  });

  it("manage_issue_relations clear_parent sends an explicit null (the server's clear marker)", async () => {
    const client = fakeClient();
    const tool = findTool("manage_issue_relations");
    await tool?.handler({ workspace: WS, issue: "VOI-1", action: "clear_parent" }, client);
    const [, body] = callsOf(client)[0]?.args as [string, Record<string, unknown>];
    expect(body).toHaveProperty("parent_issue_id", null);
  });

  it("manage_issue_relations add_relation forwards the semantic type and lock", async () => {
    const client = fakeClient();
    const tool = findTool("manage_issue_relations");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", action: "add_relation", relation_type: "blocked_by", target_issue: "VOI-2" },
      client,
    )) as Record<string, unknown>;
    const [body] = callsOf(client)[0]?.args as [Record<string, unknown>];
    // blocked_by passes through in the caller's frame; the server normalizes
    // it to a forward blocks row.
    expect(body.type).toBe("blocked_by");
    expect(body.target_issue_id).toBe("VOI-2");
    expect(result.revision).toBe(4);
    expect(String(result.note)).toMatch(/never dispatch/i);
  });

  it("manage_issue_relations remove_relation carries the optional lock in the query", async () => {
    const client = fakeClient();
    const tool = findTool("manage_issue_relations");
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-1", action: "remove_relation", relation_type: "relates_to", target_issue: "VOI-2", expected_revision: 4 },
      client,
    )) as Record<string, unknown>;
    const [relationType, targetIssueId, expectedRevision] = callsOf(client)[0]?.args as [
      string,
      string,
      number | undefined,
    ];
    expect(relationType).toBe("relates_to");
    expect(targetIssueId).toBe("VOI-2");
    expect(expectedRevision).toBe(4);
    expect(result.updated).toBe(true);
  });

  it("manage_issue_relations validates the action/relation_type/target matrix", async () => {
    const tool = findTool("manage_issue_relations");
    const client = fakeClient();
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1", action: "set_parent" }, client),
    ).rejects.toThrow(/target_issue.*required/i);
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1", action: "clear_parent", target_issue: "VOI-2" }, client),
    ).rejects.toThrow(/must be omitted/i);
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1", action: "add_relation", target_issue: "VOI-2" }, client),
    ).rejects.toThrow(/relation_type.*required/i);
    await expect(
      tool?.handler(
        { workspace: WS, issue: "VOI-1", action: "add_relation", relation_type: "enemies_with", target_issue: "VOI-2" },
        client,
      ),
    ).rejects.toThrow(/must be one of/i);
    await expect(
      tool?.handler({ workspace: WS, issue: "VOI-1", action: "remove_relation", relation_type: "blocks" }, client),
    ).rejects.toThrow(/target_issue.*required/i);
  });

  it("relation tool schemas state the no-run side effect explicitly", () => {
    const read = findTool("get_issue_relations");
    expect(read?.description).toMatch(/read-only/i);
    const manage = findTool("manage_issue_relations");
    expect(manage?.description).toMatch(/NEVER dispatches, wakes, or queues an agent run/i);
    expect(manage?.description).toMatch(/revision_conflict/i);
    expect(manage?.inputSchema.properties.action?.enum).toEqual([
      "set_parent",
      "clear_parent",
      "add_relation",
      "remove_relation",
    ]);
    expect(manage?.inputSchema.properties.relation_type?.enum).toEqual([
      "blocks",
      "blocked_by",
      "relates_to",
      "supersedes",
      "superseded_by",
    ]);
  });
});

describe("bulk_update_issues", () => {
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

describe("comment management tools", () => {
  function commentFixture(over: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
    return {
      id: "c1",
      issue_id: "i1",
      author_type: "agent",
      author_id: "a1",
      parent_id: null,
      created_at: "2026-10-03T01:00:00Z",
      updated_at: "2026-10-03T01:00:00Z",
      revision: 1,
      content: "first draft",
      ...over,
    };
  }

  describe("list_comments", () => {
    it("passes bounded-read modes through and returns briefs with revision", async () => {
      const client = fakeClient({
        listComments: async (_ws: string, issue: string, params: Record<string, unknown>) => {
          callsOf(client).push({ method: "listComments", args: [issue, params] });
          expect(issue).toBe("VOI-1");
          return [
            commentFixture({ parent_id: undefined, revision: 3, updated_at: "2026-10-03T02:00:00Z", reply_count: 2, last_activity_at: "2026-10-03T02:30:00Z" }),
            commentFixture({ id: "c2", parent_id: "c1" }),
          ];
        },
      });
      const tool = findTool("list_comments");
      const result = (await tool?.handler(
        { workspace: WS, issue: "VOI-1", thread: "c1", tail: 10, summary: true },
        client,
      )) as { total: number; comments: Array<Record<string, unknown>> };
      expect(callsOf(client)[0]?.args).toEqual([
        "VOI-1",
        { thread: "c1", tail: 10, roots_only: undefined, summary: true, fold: undefined },
      ]);
      expect(result.total).toBe(2);
      expect(result.comments[0]?.revision).toBe(3);
      expect(result.comments[0]?.updated_at).toBe("2026-10-03T02:00:00Z");
      expect(result.comments[0]?.reply_count).toBe(2);
      expect(result.comments[0]?.last_activity_at).toBe("2026-10-03T02:30:00Z");
    });

    it("rejects exclusive mode combinations before hitting the API", async () => {
      const tool = findTool("list_comments");
      for (const args of [
        { workspace: WS, issue: "VOI-1", roots_only: true, thread: "c1" },
        { workspace: WS, issue: "VOI-1", roots_only: true, recent: 5 },
        { workspace: WS, issue: "VOI-1", roots_only: true, tail: 2 },
        { workspace: WS, issue: "VOI-1", tail: 2 },
        { workspace: WS, issue: "VOI-1", fold: true, roots_only: true },
        { workspace: WS, issue: "VOI-1", fold: true, since: "2026-10-01T00:00:00Z" },
        { workspace: WS, issue: "VOI-1", fold: true, thread: "c1", tail: 3 },
      ]) {
        await expect(tool?.handler(args, fakeClient())).rejects.toThrow(ToolInputError);
      }
    });
  });

  describe("get_comment", () => {
    it("locates one comment via the thread read and reports its audit fields", async () => {
      const client = fakeClient({
        listComments: async (_ws: string, _issue: string, params: Record<string, unknown>) => {
          callsOf(client).push({ method: "listComments", args: [params] });
          return [
            commentFixture({ parent_id: undefined }),
            commentFixture({ id: "c2", parent_id: "c1" }),
          ];
        },
      });
      const tool = findTool("get_comment");
      const result = (await tool?.handler(
        { workspace: WS, issue: "VOI-1", comment_id: "c2" },
        client,
      )) as { found: boolean; comment: Record<string, unknown> };
      expect(callsOf(client)[0]?.args).toEqual([{ thread: "c2" }]);
      expect(result.found).toBe(true);
      expect(result.comment.id).toBe("c2");
      expect(result.comment.thread_root_id).toBe("c1");
      expect(result.comment.revision).toBe(1);
      expect(result.comment.content).toBe("first draft");
    });

    it("maps an unknown/deleted anchor to the structured not_found outcome", async () => {
      const client = fakeClient({
        listComments: async () => {
          throw new MulticaApiError(404, "thread anchor not found in this issue");
        },
      });
      const result = (await findTool("get_comment")?.handler(
        { workspace: WS, issue: "VOI-1", comment_id: "gone" },
        client,
      )) as { found: boolean; code: string; comment: unknown };
      expect(result.found).toBe(false);
      expect(result.code).toBe("not_found");
      expect(result.comment).toBeNull();
    });

    it("answers not_found when the anchor id is absent from the resolved thread", async () => {
      const client = fakeClient({
        listComments: async () => [commentFixture({ id: "other", parent_id: undefined })],
      });
      const result = (await findTool("get_comment")?.handler(
        { workspace: WS, issue: "VOI-1", comment_id: "c1" },
        client,
      )) as { found: boolean; code: string };
      expect(result.found).toBe(false);
      expect(result.code).toBe("not_found");
    });
  });

  describe("edit_comment", () => {
    it("maps the edit fields onto the PUT body and returns the audit snapshot", async () => {
      const client = fakeClient({
        updateComment: async (_ws: string, id: string, body: Record<string, unknown>) => {
          callsOf(client).push({ method: "updateComment", args: [id, body] });
          return commentFixture({
            content: String(body.content),
            revision: 2,
            updated_at: "2026-10-03T03:00:00Z",
            trigger_outcomes: [{ target_type: "agent", target_id: "a1", status: "queued" }],
          });
        },
      });
      const tool = findTool("edit_comment");
      const result = (await tool?.handler(
        {
          workspace: WS,
          comment_id: "c1",
          content: "revised body",
          expected_revision: 1,
          suppress_agent_ids: ["a2"],
        },
        client,
      )) as Record<string, unknown>;
      expect(callsOf(client)[0]?.args).toEqual([
        "c1",
        { content: "revised body", expected_revision: 1, suppress_agent_ids: ["a2"] },
      ]);
      expect(result.edited).toBe(true);
      expect(result.id).toBe("c1");
      expect(result.revision).toBe(2);
      expect(result.updated_at).toBe("2026-10-03T03:00:00Z");
      expect(result.trigger_outcomes).toEqual([
        { target_type: "agent", target_id: "a1", status: "queued" },
      ]);
    });

    it("reports zero dispatches for an ordinary content edit (no implicit run surface)", async () => {
      // Backend contract: a content-changing edit re-runs the trigger
      // computation, and an edit that stores identical content triggers
      // nothing — both arrive here as a response without trigger_outcomes.
      // The tool normalizes that to a visible empty array so "no runs" is an
      // asserted surface, never an absent field.
      const client = fakeClient({
        updateComment: async () => commentFixture({ revision: 2 }),
      });
      const result = (await findTool("edit_comment")?.handler(
        { workspace: WS, comment_id: "c1", content: "typo fix" },
        client,
      )) as { edited: boolean; trigger_outcomes: unknown[] };
      expect(result.edited).toBe(true);
      expect(result.trigger_outcomes).toEqual([]);
    });

    it("surfaces a revision conflict structurally with the current revision", async () => {
      const conflict = new MulticaApiError(409, "revision_conflict: resource changed since it was loaded");
      (conflict as { body?: Record<string, unknown> }).body = {
        code: "revision_conflict",
        expected_revision: 1,
        actual_revision: 4,
      };
      const client = fakeClient({
        updateComment: async () => {
          throw conflict;
        },
      });
      const result = (await findTool("edit_comment")?.handler(
        { workspace: WS, comment_id: "c1", content: "x", expected_revision: 1 },
        client,
      )) as Record<string, unknown>;
      expect(result.edited).toBe(false);
      expect(result.code).toBe("revision_conflict");
      expect(result.expected_revision).toBe(1);
      expect(result.actual_revision).toBe(4);
    });

    it("surfaces a permission denial as a distinct structured outcome (removing the mapping fails this)", async () => {
      // Valid-permission assertion: the denial is keyed on the 403 STATUS, not
      // on message text, and is returned as code permission_denied — a
      // different denial must not collapse into it, and without the mapping
      // the handler would throw instead of answering structurally.
      const client = fakeClient({
        updateComment: async () => {
          const denial = new MulticaApiError(403, "only comment author or admin can edit");
          (denial as { body?: Record<string, unknown> }).body = {
            error: "only comment author or admin can edit",
          };
          throw denial;
        },
      });
      const result = (await findTool("edit_comment")?.handler(
        { workspace: WS, comment_id: "c1", content: "x" },
        client,
      )) as Record<string, unknown>;
      expect(result.edited).toBe(false);
      expect(result.code).toBe("permission_denied");

      // A non-403 failure with the SAME message text must NOT be classified
      // as a permission denial — the status is the discriminator.
      const misclassified = new MulticaApiError(500, "only comment author or admin can edit");
      const broken = fakeClient({
        updateComment: async () => {
          throw misclassified;
        },
      });
      await expect(
        findTool("edit_comment")?.handler(
          { workspace: WS, comment_id: "c1", content: "x" },
          broken,
        ),
      ).rejects.toThrow(MulticaApiError);
    });

    it("surfaces blocked mention admission with the invalid spans", async () => {
      const denial = new MulticaApiError(422, "invalid_agent_mentions: one or more agent mentions cannot be invoked");
      (denial as { body?: Record<string, unknown> }).body = {
        error: "one or more agent mentions cannot be invoked",
        code: "invalid_agent_mentions",
        invalid_mentions: [{ start: 3, end: 40 }],
      };
      const client = fakeClient({
        updateComment: async () => {
          throw denial;
        },
      });
      const result = (await findTool("edit_comment")?.handler(
        { workspace: WS, comment_id: "c1", content: "@ghost" },
        client,
      )) as Record<string, unknown>;
      expect(result.edited).toBe(false);
      expect(result.code).toBe("invalid_mentions");
      expect(result.invalid_mentions).toEqual([{ start: 3, end: 40 }]);
    });

    it("distinguishes an already-deleted comment as not_found", async () => {
      const client = fakeClient({
        updateComment: async () => {
          const gone = new MulticaApiError(404, "comment not found");
          (gone as { body?: Record<string, unknown> }).body = { error: "comment not found" };
          throw gone;
        },
      });
      const result = (await findTool("edit_comment")?.handler(
        { workspace: WS, comment_id: "c1", content: "x" },
        client,
      )) as Record<string, unknown>;
      expect(result.edited).toBe(false);
      expect(result.code).toBe("not_found");
    });

    it("rejects expected_revision below 1 before hitting the API", async () => {
      const tool = findTool("edit_comment");
      await expect(
        tool?.handler(
          { workspace: WS, comment_id: "c1", content: "x", expected_revision: 0 },
          fakeClient(),
        ),
      ).rejects.toThrow(ToolInputError);
    });
  });

  describe("delete_comment", () => {
    it("deletes by id and returns the confirmation", async () => {
      const client = fakeClient({
        deleteComment: async (_ws: string, id: string) => {
          callsOf(client).push({ method: "deleteComment", args: [id] });
          return undefined;
        },
      });
      const result = (await findTool("delete_comment")?.handler(
        { workspace: WS, comment_id: "c1" },
        client,
      )) as Record<string, unknown>;
      expect(callsOf(client)[0]?.args).toEqual(["c1"]);
      expect(result.deleted).toBe(true);
      expect(result.id).toBe("c1");
    });

    it("surfaces permission denial and already-deleted as distinct structured outcomes", async () => {
      const forbidden = new MulticaApiError(403, "only comment author or admin can delete");
      (forbidden as { body?: Record<string, unknown> }).body = {
        error: "only comment author or admin can delete",
      };
      const deniedClient = fakeClient({
        deleteComment: async () => {
          throw forbidden;
        },
      });
      const denied = (await findTool("delete_comment")?.handler(
        { workspace: WS, comment_id: "c1" },
        deniedClient,
      )) as Record<string, unknown>;
      expect(denied.deleted).toBe(false);
      expect(denied.code).toBe("permission_denied");

      const gone = new MulticaApiError(404, "comment not found");
      (gone as { body?: Record<string, unknown> }).body = { error: "comment not found" };
      const goneClient = fakeClient({
        deleteComment: async () => {
          throw gone;
        },
      });
      const deleted = (await findTool("delete_comment")?.handler(
        { workspace: WS, comment_id: "c1" },
        goneClient,
      )) as Record<string, unknown>;
      expect(deleted.deleted).toBe(false);
      expect(deleted.code).toBe("not_found");
      // The two outcomes stay structurally distinguishable.
      expect(deleted.code).not.toBe(denied.code);
    });

    it("rethrows failures outside the defined outcome matrix", async () => {
      const client = fakeClient({
        deleteComment: async () => {
          throw new MulticaApiError(500, "failed to delete comment");
        },
      });
      await expect(
        findTool("delete_comment")?.handler({ workspace: WS, comment_id: "c1" }, client),
      ).rejects.toThrow(MulticaApiError);
    });
  });
});

// ---- Workspace run view + agent/squad management ----------------

describe("workspace run view + agent/squad management tools", () => {
  const agentDetail = {
    id: "a1",
    name: "Worker",
    description: "does things",
    instructions: "be careful",
    runtime_id: "rt1",
    runtime_bound: true,
    model: "gpt-test",
    thinking_level: "high",
    service_tier: "",
    max_concurrent_tasks: 2,
    permission_mode: "private",
    visibility: "workspace",
    status: "active",
    owner_id: "u1",
    // The real Go response carries these secret-bearing fields; the tool must
    // never project them into its output.
    runtime_config: { gateway: { token: "supersecret" } },
    mcp_config: { mcpServers: { x: { url: "https://example.com/?token=supersecret" } } },
    custom_env: { API_KEY: "supersecret" },
    composio_toolkit_allowlist: ["github"],
    has_custom_env: true,
    custom_env_key_count: 1,
    mcp_config_redacted: false,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    archived_at: null,
  };

  it("list_runs passes filters through and projects workspace rows", async () => {
    const client = fakeClient({
      listWorkspaceRuns: async (_ws: string, params: Record<string, unknown>) => {
        callsOf(client).push({ method: "listWorkspaceRuns", args: [params] });
        return {
          runs: [
            {
              id: "t1",
              status: "running",
              agent_id: "a1",
              issue_id: "i1",
              issue_identifier: "VOI-1",
              issue_title: "First issue",
              trigger: "comment",
              created_at: "2026-10-04T10:00:00Z",
              failure_reason: "",
            },
          ],
          count: 1,
          has_more: true,
          next_offset: 1,
        };
      },
    });
    const tool = findTool("list_runs");
    const result = (await tool?.handler(
      {
        workspace: WS,
        status: "pending,failed",
        agent_id: "a1",
        issue: "VOI-1",
        trigger: "comment",
        created_after: "2026-10-04T09:00:00Z",
        limit: 50,
      },
      client,
    )) as Record<string, unknown>;
    expect(callsOf(client)[0]).toEqual({
      method: "listWorkspaceRuns",
      args: [
        {
          status: "pending,failed",
          agent_id: "a1",
          project_id: undefined,
          issue: "VOI-1",
          trigger: "comment",
          created_after: "2026-10-04T09:00:00Z",
          created_before: undefined,
          limit: 50,
          offset: undefined,
        },
      ],
    });
    expect(result.total).toBe(1);
    expect(result.has_more).toBe(true);
    expect(result.next_offset).toBe(1);
    const row = (result.runs as Array<Record<string, unknown>>)[0]!;
    expect(row.issue).toBe("VOI-1");
    expect(row.trigger).toBe("comment");
  });

  it("list_runs consumes the server's wrapper payload over the real client (contract drift guard)", async () => {
    let requested = "";
    const fetchImpl = (async (input: RequestInfo | URL) => {
      requested = String(input);
      return new Response(
        JSON.stringify({
          runs: [{ id: "t1", status: "queued", agent_id: "a1", trigger: "other" }],
          count: 1,
          has_more: false,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    }) as typeof fetch;
    const client = new MulticaClient({ serverUrl: "https://api.example.com", token: "mul_test", fetchImpl });
    const result = (await findTool("list_runs")?.handler(
      { workspace: WS, status: "queued", limit: 10 },
      client,
    )) as { total: number };
    const url = new URL(requested);
    expect(url.pathname).toBe("/api/task-runs");
    expect(url.searchParams.get("status")).toBe("queued");
    expect(url.searchParams.get("limit")).toBe("10");
    expect(result.total).toBe(1);
  });

  it("list_runs rejects a malformed time bound before hitting the API", async () => {
    const client = fakeClient({
      listWorkspaceRuns: async () => {
        throw new Error("must not be called");
      },
    });
    await expect(
      findTool("list_runs")?.handler({ workspace: WS, created_after: "not-a-time" }, client),
    ).rejects.toThrow(ToolInputError);
  });

  it("get_agent projects metadata only — no secret-bearing value reaches the output", async () => {
    const client = fakeClient({ getAgent: async () => agentDetail });
    const result = (await findTool("get_agent")?.handler(
      { workspace: WS, agent_id: "a1" },
      client,
    )) as { agent: Record<string, unknown> };
    // get_agent returns the projection at the top level.
    const brief = result as unknown as Record<string, unknown>;
    const text = JSON.stringify(brief);
    expect(text).not.toContain("supersecret");
    // No secret-bearing container key survives the projection; the only
    // allowed mentions are the boolean/count indicator fields themselves.
    for (const forbidden of ["mcp_config", "runtime_config", "custom_env", "composio_toolkit"]) {
      expect(Object.keys(brief), `key ${forbidden} must not survive`).not.toContain(forbidden);
    }
    expect(brief.has_custom_env).toBe(true);
    expect(brief.custom_env_key_count).toBe(1);
    expect(brief.mcp_config_redacted).toBe(false);
    expect(brief.runtime_bound).toBe(true);
  });

  it("create_agent maps required fields and never sends secret-bearing keys", async () => {
    const client = fakeClient({
      createAgent: async (_ws: string, body: Record<string, unknown>) => {
        callsOf(client).push({ method: "createAgent", args: [body] });
        return { ...agentDetail, name: String(body.name) };
      },
    });
    const result = (await findTool("create_agent")?.handler(
      { workspace: WS, name: "New agent", runtime_id: "rt1", instructions: "hi" },
      client,
    )) as Record<string, unknown>;
    const body = callsOf(client)[0]?.args[0] as Record<string, unknown>;
    expect(body).toEqual({
      name: "New agent",
      runtime_id: "rt1",
      description: "",
      instructions: "hi",
      model: "",
      thinking_level: "",
      max_concurrent_tasks: undefined,
    });
    expect(Object.keys(body)).not.toContain("custom_env");
    expect(Object.keys(body)).not.toContain("mcp_config");
    expect(Object.keys(body)).not.toContain("runtime_config");
    expect(result.created).toBe(true);
  });

  it("create_agent requires name and runtime_id", async () => {
    const client = fakeClient({});
    await expect(
      findTool("create_agent")?.handler({ workspace: WS, name: "x" }, client),
    ).rejects.toThrow(ToolInputError);
    await expect(
      findTool("create_agent")?.handler({ workspace: WS, runtime_id: "rt1" }, client),
    ).rejects.toThrow(ToolInputError);
  });

  it("update_agent sends only the provided keys (PATCH semantics)", async () => {
    const client = fakeClient({
      updateAgent: async (_ws: string, id: string, body: Record<string, unknown>) => {
        callsOf(client).push({ method: "updateAgent", args: [id, body] });
        return { ...agentDetail, name: String(body.name) };
      },
    });
    await findTool("update_agent")?.handler(
      { workspace: WS, agent_id: "a1", name: "Renamed" },
      client,
    );
    expect(callsOf(client)[0]).toEqual({
      method: "updateAgent",
      args: ["a1", { name: "Renamed" }],
    });
  });

  it("archive_agent reports the side effect and maps 409 to already_archived", async () => {
    const ok = fakeClient({
      archiveAgent: async () => ({ ...agentDetail, archived_at: "2026-10-04T10:00:00Z" }),
    });
    const result = (await findTool("archive_agent")?.handler(
      { workspace: WS, agent_id: "a1" },
      ok,
    )) as Record<string, unknown>;
    expect(result.archived).toBe(true);
    expect(String(result.note)).toMatch(/cancel/i);

    const dup = fakeClient({
      archiveAgent: async () => {
        throw new MulticaApiError(409, "agent is already archived");
      },
    });
    const repeat = (await findTool("archive_agent")?.handler(
      { workspace: WS, agent_id: "a1" },
      dup,
    )) as Record<string, unknown>;
    expect(repeat.code).toBe("already_archived");
    expect(repeat.archived).toBe(false);

    const denied = fakeClient({
      archiveAgent: async () => {
        throw new MulticaApiError(403, "insufficient permissions");
      },
    });
    await expect(
      findTool("archive_agent")?.handler({ workspace: WS, agent_id: "a1" }, denied),
    ).rejects.toThrow(MulticaApiError);
  });

  it("restore_agent maps 409 to not_archived", async () => {
    const client = fakeClient({
      restoreAgent: async () => {
        throw new MulticaApiError(409, "agent is not archived");
      },
    });
    const result = (await findTool("restore_agent")?.handler(
      { workspace: WS, agent_id: "a1" },
      client,
    )) as Record<string, unknown>;
    expect(result.code).toBe("not_archived");
    expect(result.restored).toBe(false);
  });

  it("list_runtimes and list_squads project brief rows", async () => {
    const runtimes = fakeClient({
      listRuntimes: async () => [
        { id: "rt1", name: "desk", runtime_mode: "cloud", status: "online", visibility: "private" },
      ],
    });
    const rt = (await findTool("list_runtimes")?.handler({ workspace: WS }, runtimes)) as {
      total: number;
      runtimes: Array<Record<string, unknown>>;
    };
    expect(rt.total).toBe(1);
    expect(rt.runtimes[0]!.id).toBe("rt1");

    const squads = fakeClient({
      listSquads: async () => [
        {
          id: "s1",
          name: "Dev squad",
          leader_id: "a1",
          member_count: 2,
          member_preview: [],
          archived_at: null,
        },
      ],
    });
    const sq = (await findTool("list_squads")?.handler({ workspace: WS }, squads)) as {
      total: number;
      squads: Array<Record<string, unknown>>;
    };
    expect(sq.squads[0]!.leader_id).toBe("a1");
  });

  it("get_squad returns the full projection", async () => {
    const client = fakeClient({
      getSquad: async () => ({
        id: "s1",
        name: "Dev squad",
        instructions: "squad rules",
        leader_id: "a1",
        member_count: 2,
        archived_at: null,
      }),
    });
    const result = (await findTool("get_squad")?.handler(
      { workspace: WS, squad_id: "s1" },
      client,
    )) as Record<string, unknown>;
    expect(result.id).toBe("s1");
    expect(result.instructions).toBe("squad rules");
  });

  it("create_squad maps name/leader_id and create never triggers a run", async () => {
    const client = fakeClient({
      createSquad: async (_ws: string, body: Record<string, unknown>) => {
        callsOf(client).push({ method: "createSquad", args: [body] });
        return { id: "s1", name: String(body.name), leader_id: body.leader_id };
      },
    });
    const result = (await findTool("create_squad")?.handler(
      { workspace: WS, name: "New squad", leader_id: "a1" },
      client,
    )) as Record<string, unknown>;
    expect(callsOf(client)[0]?.args[0]).toEqual({
      name: "New squad",
      leader_id: "a1",
      description: "",
    });
    expect(result.created).toBe(true);
  });

  it("update_squad sends only the provided keys", async () => {
    const client = fakeClient({
      updateSquad: async (_ws: string, id: string, body: Record<string, unknown>) => {
        callsOf(client).push({ method: "updateSquad", args: [id, body] });
        return { id, name: String(body.name) };
      },
    });
    await findTool("update_squad")?.handler(
      { workspace: WS, squad_id: "s1", instructions: "new rules" },
      client,
    );
    expect(callsOf(client)[0]).toEqual({
      method: "updateSquad",
      args: ["s1", { instructions: "new rules" }],
    });
  });

  it("archive_squad maps the already-archived answer and reports the transfer side effect", async () => {
    const ok = fakeClient({ archiveSquad: async () => undefined });
    const result = (await findTool("archive_squad")?.handler(
      { workspace: WS, squad_id: "s1" },
      ok,
    )) as Record<string, unknown>;
    expect(result.archived).toBe(true);
    expect(String(result.note)).toMatch(/leader/i);

    const dup = fakeClient({
      archiveSquad: async () => {
        throw new MulticaApiError(400, "squad is already archived");
      },
    });
    const repeat = (await findTool("archive_squad")?.handler(
      { workspace: WS, squad_id: "s1" },
      dup,
    )) as Record<string, unknown>;
    expect(repeat.code).toBe("already_archived");
    expect(repeat.archived).toBe(false);
  });

  it("archive tools declare their destructive side effects in the description", () => {
    expect(findTool("archive_agent")?.description).toMatch(/CANCEL/i);
    expect(findTool("archive_squad")?.description).toMatch(/REASSIGNED/i);
    expect(findTool("archive_squad")?.description).toMatch(/no restore/i);
    // Reads stay inert and say so.
    for (const name of ["list_runs", "get_agent", "list_runtimes", "list_squads", "get_squad"]) {
      expect(findTool(name)?.description).toMatch(/Read-only|never/i);
    }
    // Management writes declare the no-optimistic-lock reality.
    expect(findTool("update_agent")?.description).toMatch(/no revision field|last-write-wins/i);
    expect(findTool("update_squad")?.description).toMatch(/no revision field|last-write-wins/i);
  });
});

describe("quick reply tools", () => {
  function qrClient(): MulticaClient {
    const calls: Array<{ method: string; args: unknown[] }> = [];
    const client = {
      listQuickReplies: async () => ({
        total: 2,
        quick_replies: [
          { id: "qr1", name: "处理合并冲突", content: "我先来处理合并冲突。", position: 0 },
          { id: "qr2", name: "补充用例", content: "我来补充用例。", position: 1 },
        ],
      }),
      createQuickReply: async (_ws: string, body: Record<string, unknown>) => {
        calls.push({ method: "createQuickReply", args: [body] });
        return { id: "qr-new", name: String(body.name), content: String(body.content), position: 2 };
      },
      updateQuickReply: async (_ws: string, id: string, body: Record<string, unknown>) => {
        calls.push({ method: "updateQuickReply", args: [id, body] });
        return { id, name: "补测试 v2", content: String(body.content ?? "我来补测试。"), position: 1 };
      },
      deleteQuickReply: async (_ws: string, id: string) => {
        calls.push({ method: "deleteQuickReply", args: [id] });
      },
    };
    (client as unknown as { __calls: unknown }).__calls = calls;
    return client as unknown as MulticaClient;
  }

  it("list_quick_replies returns the workspace catalog verbatim", async () => {
    const result = (await findTool("list_quick_replies")!.handler(
      { workspace: WS },
      qrClient(),
    )) as { total: number; quick_replies: Array<{ id: string; name: string }> };
    expect(result.total).toBe(2);
    expect(result.quick_replies.map((reply) => reply.id)).toEqual(["qr1", "qr2"]);
  });

  it("create_quick_reply forwards name and content", async () => {
    const client = qrClient();
    const result = (await findTool("create_quick_reply")!.handler(
      { workspace: WS, name: "推进后续工作", content: "我继续推进这项工作。" },
      client,
    )) as { id: string; name: string };
    expect(result.id).toBe("qr-new");
    expect(result.name).toBe("推进后续工作");
    expect(callsOf(client)).toEqual([
      { method: "createQuickReply", args: [{ name: "推进后续工作", content: "我继续推进这项工作。" }] },
    ]);
  });

  it("update_quick_reply sends a PATCH body and refuses an empty one", async () => {
    const client = qrClient();
    const result = (await findTool("update_quick_reply")!.handler(
      { workspace: WS, id: "qr2", content: "我来补齐单元测试。" },
      client,
    )) as { id: string; content: string };
    expect(result.id).toBe("qr2");
    expect(callsOf(client)).toEqual([
      { method: "updateQuickReply", args: ["qr2", { content: "我来补齐单元测试。" }] },
    ]);

    await expect(
      findTool("update_quick_reply")!.handler({ workspace: WS, id: "qr2" }, client),
    ).rejects.toThrow(/at least one of/i);
  });

  it("delete_quick_reply forwards the id and reports deletion", async () => {
    const client = qrClient();
    const result = (await findTool("delete_quick_reply")!.handler(
      { workspace: WS, id: "qr1" },
      client,
    )) as { deleted: boolean; id: string };
    expect(result).toEqual({ deleted: true, id: "qr1" });
    expect(callsOf(client)).toEqual([{ method: "deleteQuickReply", args: ["qr1"] }]);
  });
});

describe("project resource tools", () => {
  function resourceFixture(over: Record<string, unknown> = {}): Record<string, unknown> {
    return {
      id: "pr-1",
      project_id: "p1",
      workspace_id: "w1",
      resource_type: "github_repo",
      resource_ref: { url: "https://github.com/cfgxy/multica-cn.git" },
      label: null,
      position: 0,
      created_at: "2026-10-05T00:00:00Z",
      created_by: "u-1",
      ...over,
    };
  }

  function resourceClient(overrides: Record<string, unknown> = {}): MulticaClient {
    const calls: Array<{ method: string; args: unknown[] }> = [];
    const client = {
      listProjectResources: async (_ws: string, projectId: string) => {
        calls.push({ method: "listProjectResources", args: [projectId] });
        return {
          total: 2,
          resources: [
            resourceFixture(),
            resourceFixture({
              id: "pr-2",
              resource_type: "local_directory",
              resource_ref: { local_path: "/home/guxy/work", daemon_id: "d-1" },
              label: "本地开发目录",
              position: 1,
            }),
          ],
        };
      },
      createProjectResource: async (_ws: string, projectId: string, body: Record<string, unknown>) => {
        calls.push({ method: "createProjectResource", args: [projectId, body] });
        return resourceFixture({ ...body, id: "pr-new", position: 2 });
      },
      updateProjectResource: async (
        _ws: string,
        projectId: string,
        resourceId: string,
        body: Record<string, unknown>,
      ) => {
        calls.push({ method: "updateProjectResource", args: [projectId, resourceId, body] });
        return resourceFixture({ id: resourceId, ...body });
      },
      deleteProjectResource: async (_ws: string, projectId: string, resourceId: string) => {
        calls.push({ method: "deleteProjectResource", args: [projectId, resourceId] });
      },
      ...overrides,
    };
    (client as unknown as { __calls: unknown }).__calls = calls;
    return client as unknown as MulticaClient;
  }

  function apiError(status: number, body: Record<string, unknown>): MulticaApiError {
    return new MulticaApiError(status, typeof body.error === "string" ? body.error : "error", body);
  }

  it("list_project_resources returns the binding list with type, ref, label and order", async () => {
    const result = (await findTool("list_project_resources")!.handler(
      { workspace: WS, project_id: "p1" },
      resourceClient(),
    )) as {
      total: number;
      resources: Array<{ id: string; resource_type: string; resource_ref: unknown; label: string | null; position: number }>;
    };
    expect(result.total).toBe(2);
    expect(result.resources[0]).toMatchObject({
      id: "pr-1",
      resource_type: "github_repo",
      resource_ref: { url: "https://github.com/cfgxy/multica-cn.git" },
      label: null,
      position: 0,
    });
    expect(result.resources[1]).toMatchObject({
      id: "pr-2",
      resource_type: "local_directory",
      label: "本地开发目录",
      position: 1,
    });
  });

  it("create_project_resource forwards the type-discriminated ref verbatim", async () => {
    const client = resourceClient();
    const result = (await findTool("create_project_resource")!.handler(
      {
        workspace: WS,
        project_id: "p1",
        resource_type: "local_directory",
        resource_ref: { local_path: "/home/guxy/work", daemon_id: "d-1", execution_mode: "worktree" },
        label: "工作副本",
      },
      client,
    )) as { created: boolean; id: string; resource_type: string };
    expect(result.created).toBe(true);
    expect(result.resource_type).toBe("local_directory");
    expect(callsOf(client)).toEqual([
      {
        method: "createProjectResource",
        args: [
          "p1",
          {
            resource_type: "local_directory",
            resource_ref: { local_path: "/home/guxy/work", daemon_id: "d-1", execution_mode: "worktree" },
            label: "工作副本",
            position: undefined,
          },
        ],
      },
    ]);
  });

  it("create_project_resource validates the ref against the declared type", async () => {
    const client = resourceClient();
    await expect(
      findTool("create_project_resource")!.handler(
        {
          workspace: WS,
          project_id: "p1",
          resource_type: "github_repo",
          resource_ref: { local_path: "/home/guxy/work", daemon_id: "d-1" },
        },
        client,
      ),
    ).rejects.toBeInstanceOf(ToolInputError);
    await expect(
      findTool("create_project_resource")!.handler(
        {
          workspace: WS,
          project_id: "p1",
          resource_type: "local_directory",
          resource_ref: { local_path: "/home/guxy/work" },
        },
        client,
      ),
    ).rejects.toThrow(/daemon_id/);
    // Zero wire traffic for both refusals.
    expect(callsOf(client)).toEqual([]);
  });

  it("create_project_resource answers a duplicate binding with a structured already_attached", async () => {
    const client = resourceClient({
      createProjectResource: async () => {
        throw apiError(409, { error: "this resource is already attached to the project" });
      },
    });
    const result = (await findTool("create_project_resource")!.handler(
      {
        workspace: WS,
        project_id: "p1",
        resource_type: "github_repo",
        resource_ref: { url: "https://github.com/cfgxy/multica-cn.git" },
      },
      client,
    )) as { created: boolean; code: string; hint?: string };
    expect(result.created).toBe(false);
    expect(result.code).toBe("already_attached");
    expect(result.hint).toMatch(/list_project_resources/);
  });

  it("update_project_resource sends a PATCH body with only the provided fields", async () => {
    const client = resourceClient();
    const result = (await findTool("update_project_resource")!.handler(
      { workspace: WS, project_id: "p1", resource_id: "pr-1", label: "主仓库" },
      client,
    )) as { updated: boolean; label: string | null };
    expect(result.updated).toBe(true);
    expect(result.label).toBe("主仓库");
    expect(callsOf(client)).toEqual([
      { method: "updateProjectResource", args: ["p1", "pr-1", { label: "主仓库" }] },
    ]);
  });

  it("update_project_resource clears the label on null or empty string", async () => {
    const client = resourceClient();
    await findTool("update_project_resource")!.handler(
      { workspace: WS, project_id: "p1", resource_id: "pr-1", label: null },
      client,
    );
    expect(callsOf(client)).toEqual([
      { method: "updateProjectResource", args: ["p1", "pr-1", { label: null }] },
    ]);
  });

  it("update_project_resource refuses an empty body and forbids resource_type", async () => {
    const client = resourceClient();
    await expect(
      findTool("update_project_resource")!.handler(
        { workspace: WS, project_id: "p1", resource_id: "pr-1" },
        client,
      ),
    ).rejects.toThrow(/at least one of/i);
    await expect(
      findTool("update_project_resource")!.handler(
        {
          workspace: WS,
          project_id: "p1",
          resource_id: "pr-1",
          resource_type: "local_directory",
          label: "x",
        },
        client,
      ),
    ).rejects.toThrow(/immutable/);
    expect(callsOf(client)).toEqual([]);
  });

  it("update_project_resource maps defined server failures to structured codes", async () => {
    const notFound = resourceClient({
      updateProjectResource: async () => {
        throw apiError(404, { error: "project resource not found" });
      },
    });
    const result = (await findTool("update_project_resource")!.handler(
      { workspace: WS, project_id: "p1", resource_id: "pr-x", label: "x" },
      notFound,
    )) as { updated: boolean; code: string };
    expect(result).toMatchObject({ updated: false, code: "not_found" });

    const daemonGate = resourceClient({
      updateProjectResource: async () => {
        throw apiError(422, {
          error: "local_directory does not support worktree",
          code: "daemon_version_unsupported",
          daemon_id: "d-1",
          min_version: "0.4.30",
        });
      },
    });
    const gated = (await findTool("update_project_resource")!.handler(
      {
        workspace: WS,
        project_id: "p1",
        resource_id: "pr-2",
        resource_ref: { local_path: "/home/guxy/work", daemon_id: "d-1", execution_mode: "worktree" },
      },
      daemonGate,
    )) as { updated: boolean; code: string; min_version: string | null };
    expect(gated).toMatchObject({
      updated: false,
      code: "daemon_version_unsupported",
      min_version: "0.4.30",
    });
  });

  it("delete_project_resource forwards the binding id and reports deletion", async () => {
    const client = resourceClient();
    const result = (await findTool("delete_project_resource")!.handler(
      { workspace: WS, project_id: "p1", resource_id: "pr-1" },
      client,
    )) as { deleted: boolean; id: string };
    expect(result).toEqual({ deleted: true, id: "pr-1" });
    expect(callsOf(client)).toEqual([
      { method: "deleteProjectResource", args: ["p1", "pr-1"] },
    ]);
  });

  it("delete_project_resource answers a repeat unbind with a structured not_found", async () => {
    const client = resourceClient({
      deleteProjectResource: async () => {
        throw apiError(404, { error: "project resource not found" });
      },
    });
    const result = (await findTool("delete_project_resource")!.handler(
      { workspace: WS, project_id: "p1", resource_id: "pr-1" },
      client,
    )) as { deleted: boolean; code: string };
    expect(result).toMatchObject({ deleted: false, code: "not_found" });
  });

  it("pins the safety prose: unbind never touches the real repo or directory", () => {
    expect(findTool("delete_project_resource")?.description).toMatch(/never deleted or modified/);
    expect(findTool("delete_project_resource")?.description).toMatch(/GitHub/i);
    expect(findTool("delete_project_resource")?.description).toMatch(/local directory/i);
    // resource_type is immutable server-side: the update schema must not
    // even declare the key.
    const updateProps = findTool("update_project_resource")?.inputSchema
      .properties as Record<string, unknown>;
    expect(updateProps.resource_type).toBeUndefined();
  });
});
