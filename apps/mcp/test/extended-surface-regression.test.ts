/**
 * Extended-surface combined regression (RUYI-399, scope round 2).
 *
 * Round 1 (combined-regression.test.ts) pinned the RUYI-350/351/353 union.
 * This file extends the same cross-tool discipline to the rest of the
 * week's MCP capability surface — RUYI-282 (assign_issue), RUYI-292 (run
 * management), RUYI-352 (comment management), RUYI-354 (project
 * management) — and pins the cross-tool contracts their merges could have
 * broken:
 *   - every tool is registered once, and readOnlyHint matches the pure-read
 *     set exactly across the whole tools/list (list_projects/get_project
 *     included — same defect class as round 1's get_issue_relations);
 *   - one expected_revision floor (>=1, rejected client-side at 0) across
 *     every CAS tool, and stale revisions answer STRUCTURED
 *     revision_conflict results on the comment and project faces just like
 *     the issue face;
 *   - nullable PATCH clearing on the project face is typed string|null the
 *     way its own description promises, and explicit nulls survive to the
 *     wire;
 *   - the run side-effect boundary: the tools that can trigger runs carry
 *     suppress_run and echo run_suppressed; comment, project, relation and
 *     metadata writes structurally cannot carry run triggers; bulk items
 *     and assign_issue speak one wire contract for the same assignment;
 *   - run management (list/get/cancel/retry) keeps its documented outcome
 *     shapes, including cancel's 409 → not_cancellable;
 *   - one issue takes update_issue → assign_issue → relations → comments
 *     in series with revisions and read-back views agreeing.
 *
 * FakeRestBackend mirrors the Go handlers' observable contract for the
 * endpoints these tools route; server/internal/handler/*.go remain the
 * sources of truth.
 */

import { describe, expect, it } from "vitest";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";

import type { Logger } from "../src/log.js";
import { MulticaClient } from "../src/rest.js";
import { ToolInputError } from "../src/schemas.js";
import { findTool, TOOL_DEFINITIONS } from "../src/tools.js";
import { createMcpServer } from "../src/server.js";

const WS = "voice-notes";

/**
 * The audit's independent enumeration of the pure-read tools: handlers that
 * only GET (get_comment reads via the listComments thread resolution) or
 * compose GETs locally (progress_digest). Everything else writes — or, for
 * cancel_run/retry_run/dispatch_agent, triggers real runs.
 */
const PURE_READ_TOOLS = [
  "list_workspaces",
  "list_agents",
  "list_projects",
  "get_project",
  "list_issues",
  "get_issue",
  "search_issues",
  "progress_digest",
  "list_comments",
  "get_comment",
  "get_issue_relations",
  "list_issue_runs",
  "get_run",
  "search_audit_events",
  // RUYI-419 workspace management reads.
  "list_runs",
  "get_agent",
  "list_runtimes",
  "list_squads",
  "get_squad",
] as const;

const CAS_TOOLS = [
  "update_issue",
  "update_issue_status",
  "assign_issue",
  "manage_issue_relations",
  "update_project",
  "edit_comment",
] as const;

// ---- Part A: registration surface & readOnlyHint audit -------------------

describe("whole-surface registration and readOnlyHint audit (RUYI-399 round 2)", () => {
  it("registers every tool exactly once with a real description and an object schema", () => {
    const names = TOOL_DEFINITIONS.map((tool) => tool.name);
    expect(new Set(names).size).toBe(names.length);
    expect(names).toHaveLength(40);
    for (const definition of TOOL_DEFINITIONS) {
      expect(
        definition.description.length,
        `${definition.name}.description`,
      ).toBeGreaterThan(40);
      expect(definition.inputSchema.type).toBe("object");
      const schema = definition.inputSchema as {
        properties?: Record<string, unknown>;
        required?: string[];
      };
      for (const key of schema.required ?? []) {
        expect(schema.properties?.[key], `${definition.name}.${key}`).toBeDefined();
      }
    }
  });

  it("readOnlyHint splits tools/list exactly into the pure-read set and its complement", async () => {
    const { client, cleanup } = await connectViaMcp(new FakeRestBackend());
    try {
      const { tools } = await client.listTools();
      expect(tools).toHaveLength(40);
      const hint = Object.fromEntries(
        tools.map((tool) => [tool.name, tool.annotations?.readOnlyHint === true]),
      );
      for (const name of PURE_READ_TOOLS) {
        expect(hint[name], `${name} must be readOnlyHint=true`).toBe(true);
      }
      const readSet = new Set<string>(PURE_READ_TOOLS);
      for (const tool of tools) {
        if (!readSet.has(tool.name)) {
          expect(hint[tool.name], `${tool.name} must be readOnlyHint=false`).toBe(false);
        }
      }
      expect(Object.keys(hint)).toHaveLength(40);
    } finally {
      await cleanup();
    }
  });

  it("types every nullable-clearing project field string|null, as its description promises", () => {
    // update_project's prose says "Pass null to clear" for seven fields; a
    // strict client-side validator refuses to send null unless the schema
    // type admits it — same contract the issue face already honors
    // (update_issue.parent_issue_id is string|null).
    const props = propertiesOf("update_project");
    for (const key of [
      "description",
      "instructions",
      "icon",
      "lead_type",
      "lead_id",
      "start_date",
      "due_date",
    ]) {
      expect(props[key]?.type, `update_project.${key}.type`).toContain("null");
    }
    expect(propertiesOf("update_issue").parent_issue_id?.type).toContain("null");
  });

  it("list_projects and get_project are wire-read-only: GET requests only", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    for (const [name, args] of [
      ["list_projects", { workspace: WS }],
      ["get_project", { workspace: WS, project_id: "p-1" }],
    ] as const) {
      const result = await findTool(name)?.handler(args, client);
      expect(result).toBeDefined();
    }
    expect(backend.requestLog.length).toBeGreaterThanOrEqual(2);
    for (const entry of backend.requestLog) {
      expect(entry.method, entry.path).toBe("GET");
    }
  });
});

// ---- Part B: one expected_revision floor across the whole face -----------

describe("expected_revision floor across every CAS tool (RUYI-399 round 2)", () => {
  it("keeps schema minimum 1 on every expected_revision property anywhere in the surface", () => {
    const floors: Array<{ tool: string; minimum: unknown }> = [];
    const walk = (tool: string, schema: unknown, path: string): void => {
      if (schema === null || typeof schema !== "object") return;
      const record = schema as {
        properties?: Record<string, unknown>;
        items?: unknown;
        minimum?: unknown;
      };
      for (const [key, value] of Object.entries(record.properties ?? {})) {
        if (key === "expected_revision") {
          floors.push({ tool: `${tool}:${path}${key}`, minimum: (value as { minimum?: unknown }).minimum });
        } else {
          walk(tool, value, `${path}${key}.`);
        }
      }
      if (record.items !== undefined) walk(tool, record.items, `${path}items.`);
    };
    for (const definition of TOOL_DEFINITIONS) {
      walk(definition.name, definition.inputSchema, "");
    }
    // Six single-issue/comment/project tools + the bulk per-item schema.
    expect(floors.length).toBeGreaterThanOrEqual(7);
    for (const floor of floors) {
      expect(floor.minimum, floor.tool).toBe(1);
    }
  });

  it("rejects expected_revision 0 client-side on every CAS tool with zero wire traffic", async () => {
    for (const name of CAS_TOOLS) {
      const backend = new FakeRestBackend();
      const client = backend.client();
      const args: Record<string, unknown> = { workspace: WS, expected_revision: 0 };
      if (name === "update_issue" || name === "update_issue_status") {
        args.issue = "i-child";
        if (name === "update_issue") args.title = "x";
        else args.status = "in_progress";
      } else if (name === "assign_issue") {
        args.issue = "i-child";
        args.assignee_type = "agent";
        args.assignee_id = "a-1";
      } else if (name === "manage_issue_relations") {
        args.issue = "i-child";
        args.action = "set_parent";
        args.target_issue = "i-parent";
      } else if (name === "update_project") {
        args.project_id = "p-1";
        args.title = "x";
      } else if (name === "edit_comment") {
        args.comment_id = "c-1";
        args.content = "x";
      }
      await expect(
        findTool(name)?.handler(args, client),
      ).rejects.toBeInstanceOf(ToolInputError);
      expect(backend.requestLog, `${name} must gate at the client`).toHaveLength(0);
    }

    // The bulk item parser shares the floor: a 0 in an item dies at parse
    // time, before any per-item request leaves.
    const bulkBackend = new FakeRestBackend();
    await expect(
      findTool("bulk_update_issues")?.handler(
        {
          workspace: WS,
          updates: [{ issue: "i-child", status: "done", expected_revision: 0 }],
        },
        bulkBackend.client(),
      ),
    ).rejects.toBeInstanceOf(ToolInputError);
    expect(bulkBackend.requestLog).toHaveLength(0);
  });
});

// ---- Part C: project face (RUYI-354) --------------------------------------

describe("project face: PATCH serialization, CAS, metadata-only (RUYI-354)", () => {
  it("omitted keys stay off the wire; explicit nulls survive to the wire and clear", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    const updated = await callHandlerJson(client, "update_project", {
      workspace: WS,
      project_id: "p-1",
      title: "Renamed",
      description: null,
      start_date: null,
    });
    expect(updated.updated).toBe(true);
    expect(updated.description).toBeNull();
    expect(updated.start_date).toBeNull();
    expect(backend.requestLog.at(-1)?.body).toEqual({
      title: "Renamed",
      description: null,
      start_date: null,
    });
    expect(backend.projects.get("p-1")?.title).toBe("Renamed");
    expect(backend.projects.get("p-1")?.description).toBeNull();
    // Untouched nullable keeps its value (PATCH-by-key-presence).
    expect(backend.projects.get("p-1")?.instructions).toBe("inst");
  });

  it("lead_type/lead_id are set or cleared as a pair, never half", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    await expect(
      findTool("update_project")?.handler(
        { workspace: WS, project_id: "p-1", lead_type: "member" },
        client,
      ),
    ).rejects.toBeInstanceOf(ToolInputError);
    expect(backend.requestLog).toHaveLength(0);

    await callHandlerJson(client, "update_project", {
      workspace: WS,
      project_id: "p-1",
      lead_type: "member",
      lead_id: "u-9",
    });
    expect(backend.requestLog.at(-1)?.body).toEqual({ lead_type: "member", lead_id: "u-9" });

    await callHandlerJson(client, "update_project", {
      workspace: WS,
      project_id: "p-1",
      lead_type: null,
      lead_id: null,
    });
    expect(backend.requestLog.at(-1)?.body).toEqual({ lead_type: null, lead_id: null });
    expect(backend.projects.get("p-1")?.lead_type).toBeNull();
  });

  it("stale expected_revision answers a structured revision_conflict with no partial write", async () => {
    const backend = new FakeRestBackend();
    const { client, cleanup } = await connectViaMcp(backend);
    try {
      const conflict = await client.callTool({
        name: "update_project",
        arguments: {
          workspace: WS,
          project_id: "p-1",
          title: "lost race",
          expected_revision: 41,
        },
      });
      // A stale lock is a defined outcome, not a transport failure — the
      // same non-error structured dialect the issue and comment faces use.
      expect(conflict.isError).toBeFalsy();
      const parsed = JSON.parse(textOf(conflict)) as Record<string, unknown>;
      expect(parsed).toMatchObject({
        updated: false,
        code: "revision_conflict",
        expected_revision: 41,
        actual_revision: 1,
      });
      expect(backend.projects.get("p-1")?.title).toBe("Project One"); // nothing written

      const retried = await callToolJson(client, "update_project", {
        workspace: WS,
        project_id: "p-1",
        title: "won race",
        expected_revision: 1,
      });
      expect(retried.updated).toBe(true);
      expect(retried.revision).toBe(2);
    } finally {
      await cleanup();
    }
  });

  it("update_project stays structurally run-free: no run-trigger keys in schema or on the wire", async () => {
    const props = propertiesOf("update_project");
    for (const key of ["assignee_type", "assignee_id", "suppress_run", "handoff_note"] as const) {
      expect(props[key], `update_project.${key}`).toBeUndefined();
    }
    const backend = new FakeRestBackend();
    await callHandlerJson(backend.client(), "update_project", {
      workspace: WS,
      project_id: "p-1",
      title: "T",
      status: "in_progress", // project status — not an issue run trigger
      due_date: null,
    });
    const body = backend.requestLog.at(-1)?.body ?? {};
    expect(body.assignee_type).toBeUndefined();
    expect(body.assignee_id).toBeUndefined();
    expect(body.suppress_run).toBeUndefined();
    expect(body.handoff_note).toBeUndefined();
  });
});

// ---- Part D: comment face (RUYI-352) --------------------------------------

describe("comment face: CAS wire contract and structured failures (RUYI-352)", () => {
  it("edit_comment sends exactly the provided keys and echoes trigger_outcomes", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    const edited = await callHandlerJson(client, "edit_comment", {
      workspace: WS,
      comment_id: "c-1",
      content: "edited body",
      expected_revision: 1,
      suppress_agent_ids: ["a-1"],
    });
    expect(edited.edited).toBe(true);
    expect(edited.revision).toBe(2);
    expect(edited.trigger_outcomes).toEqual([]);
    expect(backend.requestLog.at(-1)?.body).toEqual({
      content: "edited body",
      expected_revision: 1,
      suppress_agent_ids: ["a-1"],
    });

    await callHandlerJson(client, "edit_comment", {
      workspace: WS,
      comment_id: "c-1",
      content: "second pass",
    });
    expect(backend.requestLog.at(-1)?.body).toEqual({ content: "second pass" });
  });

  it("a stale edit answers structured revision_conflict and keeps the stored content", async () => {
    const backend = new FakeRestBackend();
    const { client, cleanup } = await connectViaMcp(backend);
    try {
      const conflict = await client.callTool({
        name: "edit_comment",
        arguments: {
          workspace: WS,
          comment_id: "c-1",
          content: "lost race",
          expected_revision: 41,
        },
      });
      expect(conflict.isError).toBeFalsy();
      const parsed = JSON.parse(textOf(conflict)) as Record<string, unknown>;
      expect(parsed).toMatchObject({
        edited: false,
        code: "revision_conflict",
        expected_revision: 41,
        actual_revision: 1,
      });
      expect(backend.comments.get("c-1")?.content).toBe("first body"); // nothing written

      const retried = await callToolJson(client, "edit_comment", {
        workspace: WS,
        comment_id: "c-1",
        content: "won race",
        expected_revision: 1,
      });
      expect(retried.edited).toBe(true);
      expect(retried.revision).toBe(2);
      expect(backend.comments.get("c-1")?.content).toBe("won race");
    } finally {
      await cleanup();
    }
  });

  it("maps the comment outcome matrix: permission_denied, not_found, invalid_mentions", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    const denied = await callHandlerJson(client, "edit_comment", {
      workspace: WS,
      comment_id: "c-403",
      content: "x",
    });
    expect(denied).toMatchObject({ edited: false, code: "permission_denied" });

    const unknownComment = await callHandlerJson(client, "edit_comment", {
      workspace: WS,
      comment_id: "c-404",
      content: "x",
    });
    expect(unknownComment).toMatchObject({ edited: false, code: "not_found" });

    const invalidMentions = await callHandlerJson(client, "edit_comment", {
      workspace: WS,
      comment_id: "c-422",
      content: "x",
    });
    expect(invalidMentions).toMatchObject({
      edited: false,
      code: "invalid_mentions",
      invalid_mentions: [{ target_id: "a-ghost" }],
    });

    const deletedUnknown = await callHandlerJson(client, "delete_comment", {
      workspace: WS,
      comment_id: "c-404",
    });
    expect(deletedUnknown).toMatchObject({ deleted: false, code: "not_found" });

    const deleted = await callHandlerJson(client, "delete_comment", {
      workspace: WS,
      comment_id: "c-1",
    });
    expect(deleted).toMatchObject({ deleted: true, id: "c-1" });
    // Deleting starts no run and has no body at all.
    expect(backend.requestLog.at(-1)?.method).toBe("DELETE");
    expect(backend.requestLog.at(-1)?.body).toBeUndefined();
    expect(backend.comments.has("c-1")).toBe(false);
  });

  it("comment writes cannot carry run-trigger keys — mentions are the only dispatch surface", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    await callHandlerJson(client, "add_comment", {
      workspace: WS,
      issue: "i-child",
      content: "plain comment",
    });
    expect(backend.requestLog.at(-1)?.body).toEqual({ content: "plain comment" });
    await callHandlerJson(client, "add_comment", {
      workspace: WS,
      issue: "i-child",
      content: "reply",
      parent_id: "c-1",
    });
    expect(backend.requestLog.at(-1)?.body).toEqual({ content: "reply", parent_id: "c-1" });
    await callHandlerJson(client, "edit_comment", {
      workspace: WS,
      comment_id: "c-1",
      content: "tweak",
    });
    for (const entry of backend.requestLog) {
      const body = entry.body ?? {};
      expect(body.status, entry.path).toBeUndefined();
      expect(body.assignee_type, entry.path).toBeUndefined();
      expect(body.assignee_id, entry.path).toBeUndefined();
      expect(body.suppress_run, entry.path).toBeUndefined();
    }
    // Schema side of the same boundary: no run-trigger input exists.
    for (const name of ["add_comment", "edit_comment", "delete_comment"] as const) {
      const props = propertiesOf(name);
      expect(props.status, `${name}.status`).toBeUndefined();
      expect(props.assignee_type, `${name}.assignee_type`).toBeUndefined();
      expect(props.suppress_run, `${name}.suppress_run`).toBeUndefined();
    }
  });
});

// ---- Part E: run side-effect boundary (RUYI-282 / RUYI-350) ---------------

describe("run side-effect boundary across assign/status/bulk (RUYI-399 round 2)", () => {
  it("update_issue_status echoes run_suppressed on its result", async () => {
    const backend = new FakeRestBackend();
    const { client, cleanup } = await connectViaMcp(backend);
    try {
      const suppressed = await callToolJson(client, "update_issue_status", {
        workspace: WS,
        issue: "i-child",
        status: "in_progress",
        suppress_run: true,
      });
      expect(suppressed.updated).toBe(true);
      expect(suppressed.run_suppressed).toBe(true);
      expect(backend.issues.get("i-child")?.run_suppressed).toBe(true);

      const plain = await callToolJson(client, "update_issue_status", {
        workspace: WS,
        issue: "i-other",
        status: "in_progress",
      });
      expect(plain.run_suppressed).toBe(false);
    } finally {
      await cleanup();
    }
  });

  it("assign_issue and bulk items put one wire contract for the same assignment", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    await callHandlerJson(client, "assign_issue", {
      workspace: WS,
      issue: "i-child",
      assignee_type: "agent",
      assignee_id: "a-1",
    });
    await callHandlerJson(client, "bulk_update_issues", {
      workspace: WS,
      updates: [{ issue: "i-parent", assignee_type: "agent", assignee_id: "a-1" }],
    });
    await callHandlerJson(client, "assign_issue", {
      workspace: WS,
      issue: "i-child",
      assignee_type: "member",
      assignee_id: "u-1",
    });
    await callHandlerJson(client, "bulk_update_issues", {
      workspace: WS,
      updates: [{ issue: "i-parent", assignee_type: "member", assignee_id: "u-1" }],
    });
    await callHandlerJson(client, "assign_issue", {
      workspace: WS,
      issue: "i-child",
      assignee_type: "unassigned",
    });
    await callHandlerJson(client, "bulk_update_issues", {
      workspace: WS,
      updates: [{ issue: "i-parent", assignee_type: "unassigned" }],
    });

    const bodies = backend.requestLog
      .filter((entry) => entry.method === "PUT")
      .map((entry) => entry.body ?? {});
    expect(bodies).toHaveLength(6);
    // Pairwise identical: single-issue tool vs bulk item for the same
    // semantic write — agent, member, and the double-null unassign.
    expect(bodies[0]).toEqual(bodies[1]);
    expect(bodies[0]).toEqual({ assignee_type: "agent", assignee_id: "a-1" });
    expect(bodies[2]).toEqual(bodies[3]);
    expect(bodies[2]).toEqual({ assignee_type: "member", assignee_id: "u-1" });
    expect(bodies[4]).toEqual(bodies[5]);
    expect(bodies[4]).toEqual({ assignee_type: null, assignee_id: null });
  });

  it("assign_issue echoes run_suppressed and keeps suppress_run off the member/unassign paths", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    const agentAssign = await callHandlerJson(client, "assign_issue", {
      workspace: WS,
      issue: "i-child",
      assignee_type: "agent",
      assignee_id: "a-1",
      suppress_run: true,
    });
    expect(agentAssign.assigned).toBe(true);
    expect(agentAssign.run_suppressed).toBe(true);
    expect(backend.requestLog.at(-1)?.body).toEqual({
      assignee_type: "agent",
      assignee_id: "a-1",
      suppress_run: true,
    });

    const memberAssign = await callHandlerJson(client, "assign_issue", {
      workspace: WS,
      issue: "i-parent",
      assignee_type: "member",
      assignee_id: "u-1",
      suppress_run: true, // no effect for members — key still passes through
    });
    expect(memberAssign.run_suppressed).toBe(false);

    const unassign = await callHandlerJson(client, "assign_issue", {
      workspace: WS,
      issue: "i-child",
      assignee_type: "unassigned",
    });
    expect(unassign.assigned).toBe(false);
    expect(unassign.assignee_type).toBeNull();
  });
});

// ---- Part F: run management shapes (RUYI-292) ------------------------------

describe("run management keeps its documented outcome shapes (RUYI-292)", () => {
  it("cancel_run: queued run cancels, finished run answers not_cancellable", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    const cancelled = await callHandlerJson(client, "cancel_run", {
      workspace: WS,
      issue: "i-child",
      run_id: "r-queued",
    });
    expect(cancelled).toMatchObject({
      code: "cancelled",
      cancelled: true,
      stop_pending: false,
      task: { id: "r-queued", status: "cancelled" },
    });

    const finished = await callHandlerJson(client, "cancel_run", {
      workspace: WS,
      issue: "i-child",
      run_id: "r-done",
    });
    expect(finished).toMatchObject({
      code: "not_cancellable",
      cancelled: false,
    });
    // Both cancels POST with an empty body — no run-trigger surface.
    for (const entry of backend.requestLog) {
      expect(entry.method).toBe("POST");
      expect(entry.path).toMatch(/\/tasks\/[^/]+\/cancel$/);
      expect(entry.body).toEqual({});
    }
  });

  it("retry_run enqueues a linked new run; list/get report trigger and lineage", async () => {
    const backend = new FakeRestBackend();
    const client = backend.client();
    const retried = await callHandlerJson(client, "retry_run", {
      workspace: WS,
      issue: "i-child",
      run_id: "r-done",
    });
    expect(retried).toMatchObject({
      retried: true,
      new_run_id: "r-new-1",
      status: "queued",
    });

    const listed = await callHandlerJson(client, "list_issue_runs", {
      workspace: WS,
      issue: "i-child",
    });
    expect(listed.total).toBe(2);
    const runs = listed.runs as Array<Record<string, unknown>>;
    expect(runs[0]).toMatchObject({ id: "r-1", trigger: "comment" });
    expect(runs[1]).toMatchObject({ id: "r-2", trigger: "autopilot" });

    const detail = await callHandlerJson(client, "get_run", {
      workspace: WS,
      issue: "i-child",
      run_id: "r-1",
    });
    expect(detail).toMatchObject({
      id: "r-1",
      status: "cancel_requested",
      cancel_requested_at: "2026-10-03T10:00:00Z",
      cancel_requested_by_user_id: "u-1",
    });
    expect(detail.ancestors).toEqual([]);
    expect(detail.descendants).toEqual([]);
  });
});

// ---- Part G: one issue, four capability faces in series --------------------

describe("combined serial path: update → assign → relations → comments (RUYI-399 round 2)", () => {
  it("keeps one revision and consistent read-back views across the four faces", async () => {
    const backend = new FakeRestBackend();
    const { client, cleanup } = await connectViaMcp(backend);
    try {
      // 1. update_issue: CAS metadata edit (rev 1 → 2).
      const updated = await callToolJson(client, "update_issue", {
        workspace: WS,
        issue: "i-child",
        title: "Combined path",
        expected_revision: 1,
      });
      expect(updated.revision).toBe(2);

      // 2. assign_issue: suppressed agent handoff (rev 2 → 3, flagged).
      const assigned = await callToolJson(client, "assign_issue", {
        workspace: WS,
        issue: "i-child",
        assignee_type: "agent",
        assignee_id: "a-1",
        suppress_run: true,
      });
      expect(assigned.revision).toBe(3);
      expect(assigned.run_suppressed).toBe(true);

      // 3. manage_issue_relations: CAS edge add on the fresh revision (3 → 4).
      const related = await callToolJson(client, "manage_issue_relations", {
        workspace: WS,
        issue: "i-child",
        action: "add_relation",
        relation_type: "blocks",
        target_issue: "i-other",
        expected_revision: 3,
      });
      expect(related.revision).toBe(4);

      // 4. Comment face on the same issue: post, then CAS-edit.
      const posted = await callToolJson(client, "add_comment", {
        workspace: WS,
        issue: "i-child",
        content: "handoff context",
      });
      expect(posted.posted).toBe(true);
      const edited = await callToolJson(client, "edit_comment", {
        workspace: WS,
        comment_id: posted.id,
        content: "handoff context (edited)",
        expected_revision: 1,
      });
      expect(edited.edited).toBe(true);

      // 5. Read-backs agree: the issue and relation views carry the same
      // revision, the assignment is visible, the flag stuck, and the
      // comment edit landed — four faces, one state, no clobbering.
      const readBack = await callToolJson(client, "get_issue", {
        workspace: WS,
        issue: "i-child",
      });
      const issue = readBack.issue as Record<string, unknown>;
      expect(issue.revision).toBe(4);
      expect(issue.assignee_type).toBe("agent");
      expect(issue.run_suppressed).toBe(true);
      const thread = readBack.comments as Array<Record<string, unknown>>;
      expect(thread.some((comment) => comment.id === posted.id)).toBe(true);
      const relations = await callToolJson(client, "get_issue_relations", {
        workspace: WS,
        issue: "i-child",
      });
      expect(relations.revision).toBe(4);
      expect(relations.blocks).toEqual([expect.objectContaining({ id: "i-other" })]);
      expect(backend.comments.get(posted.id as string)?.content).toBe("handoff context (edited)");

      // Only the assign step ever carried run-trigger keys.
      const triggeringWrites = backend.requestLog.filter(
        (entry) => {
          const body = entry.body ?? {};
          return body.assignee_type !== undefined || body.status !== undefined;
        },
      );
      expect(triggeringWrites).toHaveLength(1);
      expect(triggeringWrites[0]?.body).toMatchObject({
        assignee_type: "agent",
        assignee_id: "a-1",
        suppress_run: true,
      });
    } finally {
      await cleanup();
    }
  });
});

// ---- Fake REST backend (server contract mirror) ---------------------------

interface FakeIssue {
  id: string;
  identifier: string;
  number: number;
  title: string;
  status: string;
  priority: string;
  project_id: string | null;
  parent_issue_id: string | null;
  start_date: string | null;
  due_date: string | null;
  assignee_type: string | null;
  assignee_id: string | null;
  revision: number;
  run_suppressed: boolean;
}

interface FakeComment {
  id: string;
  issue_id: string;
  content: string;
  revision: number;
  created_at: string;
  updated_at: string;
  trigger_outcomes: Array<Record<string, unknown>>;
}

interface FakeProject {
  id: string;
  workspace_id: string;
  title: string;
  description: string | null;
  instructions: string | null;
  icon: string | null;
  status: string;
  priority: string;
  lead_type: string | null;
  lead_id: string | null;
  start_date: string | null;
  due_date: string | null;
  revision: number;
  issue_count: number;
  created_at: string;
  updated_at: string;
}

interface LoggedRequest {
  method: string;
  path: string;
  body?: Record<string, unknown>;
}

const NULLABLE_ISSUE_KEYS = [
  "project_id",
  "start_date",
  "due_date",
  "parent_issue_id",
  "assignee_type",
  "assignee_id",
] as const;

const NULLABLE_PROJECT_KEYS = [
  "description",
  "instructions",
  "icon",
  "lead_type",
  "lead_id",
  "start_date",
  "due_date",
] as const;

/**
 * In-memory mirror of the Go handlers' observable contract for the extended
 * surface: PATCH-by-key-presence with explicit-null clears, positive-integer
 * expected_revision CAS answering the structured revision_conflict body, a
 * revision bump on every committed write, symmetric relation edges, and the
 * comment/project/run outcome matrices. Deliberately no permission model or
 * cycle detection — server-side concerns outside this regression's scope.
 */
class FakeRestBackend {
  issues = new Map<string, FakeIssue>();
  comments = new Map<string, FakeComment>();
  projects = new Map<string, FakeProject>();
  blocks = new Set<string>(); // forward edges "blocks:<source>:<target>"
  requestLog: LoggedRequest[] = [];
  private clock = 0;
  private commentSeq = 0;

  constructor() {
    this.issues.set("i-child", this.seedIssue("i-child", "VOI-1", "Child"));
    this.issues.set("i-parent", this.seedIssue("i-parent", "VOI-9", "Parent"));
    this.issues.set("i-other", this.seedIssue("i-other", "VOI-2", "Other"));
    this.comments.set("c-1", {
      id: "c-1",
      issue_id: "i-child",
      content: "first body",
      revision: 1,
      created_at: "2026-10-03T09:00:00Z",
      updated_at: "2026-10-03T09:00:00Z",
      trigger_outcomes: [],
    });
    this.projects.set("p-1", {
      id: "p-1",
      workspace_id: "w-1",
      title: "Project One",
      description: "desc",
      instructions: "inst",
      icon: "icon",
      status: "planned",
      priority: "high",
      lead_type: "member",
      lead_id: "u-1",
      start_date: "2026-10-01",
      due_date: null,
      revision: 1,
      issue_count: 3,
      created_at: "2026-10-01T00:00:00Z",
      updated_at: "2026-10-01T00:00:00Z",
    });
  }

  private seedIssue(id: string, identifier: string, title: string): FakeIssue {
    return {
      id,
      identifier,
      number: 1,
      title,
      status: "todo",
      priority: "none",
      project_id: null,
      parent_issue_id: null,
      start_date: null,
      due_date: null,
      assignee_type: null,
      assignee_id: null,
      revision: 1,
      run_suppressed: false,
    };
  }

  /** A real MulticaClient wired to this backend, with a full request log. */
  client(): MulticaClient {
    return new MulticaClient({
      serverUrl: "https://api.example.com",
      token: "mul_test",
      fetchImpl: this.fetchImpl(),
      logger: silentLogger(),
    });
  }

  private fetchImpl(): typeof fetch {
    return (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input));
      const method = init?.method ?? "GET";
      const path = url.pathname;
      const rawBody = init?.body === undefined ? undefined : String(init.body);
      const body =
        rawBody === undefined || rawBody.length === 0
          ? undefined
          : (JSON.parse(rawBody) as Record<string, unknown>);
      this.requestLog.push({ method, path, body });

      const putIssue = path.match(/^\/api\/issues\/([^/]+)$/);
      const relations = path.match(/^\/api\/issues\/([^/]+)\/relations$/);
      const relation = path.match(/^\/api\/issues\/([^/]+)\/relations\/([^/]+)\/([^/]+)$/);
      const addComment = path.match(/^\/api\/issues\/([^/]+)\/comments$/);
      const putComment = path.match(/^\/api\/comments\/([^/]+)$/);
      const putProject = path.match(/^\/api\/projects\/([^/]+)$/);
      const getProject = path.match(/^\/api\/projects\/([^/]+)$/);
      const taskRuns = path.match(/^\/api\/issues\/([^/]+)\/task-runs$/);
      const taskDetail = path.match(/^\/api\/issues\/([^/]+)\/tasks\/([^/]+)$/);
      const taskCancel = path.match(/^\/api\/issues\/([^/]+)\/tasks\/([^/]+)\/cancel$/);
      const taskRetry = path.match(/^\/api\/issues\/([^/]+)\/tasks\/([^/]+)\/retry$/);

      const getIssue = path.match(/^\/api\/issues\/([^/]+)$/);
      const listComments = path.match(/^\/api\/issues\/([^/]+)\/comments$/);

      let response: Response;
      if (method === "PUT" && putIssue) {
        response = this.updateIssue(decodeURIComponent(putIssue[1]!), body ?? {});
      } else if (method === "GET" && getIssue) {
        const issue = this.issues.get(decodeURIComponent(getIssue[1]!));
        response = issue ? json(issue) : json({ error: "issue not found" }, 404);
      } else if (method === "GET" && listComments) {
        const issueId = decodeURIComponent(listComments[1]!);
        response = json([...this.comments.values()].filter((c) => c.issue_id === issueId));
      } else if (method === "GET" && relations) {
        response = this.getRelations(decodeURIComponent(relations[1]!));
      } else if (method === "POST" && relations) {
        response = this.addRelation(decodeURIComponent(relations[1]!), body ?? {});
      } else if (method === "DELETE" && relation) {
        const expected = url.searchParams.get("expected_revision");
        response = this.removeRelation(
          decodeURIComponent(relation[1]!),
          decodeURIComponent(relation[2]!),
          decodeURIComponent(relation[3]!),
          expected === null ? undefined : Number(expected),
        );
      } else if (method === "POST" && addComment) {
        response = this.addComment(decodeURIComponent(addComment[1]!), body ?? {});
      } else if (method === "PUT" && putComment) {
        response = this.updateComment(decodeURIComponent(putComment[1]!), body ?? {});
      } else if (method === "DELETE" && putComment) {
        response = this.deleteComment(decodeURIComponent(putComment[1]!));
      } else if (method === "PUT" && putProject) {
        response = this.updateProject(decodeURIComponent(putProject[1]!), body ?? {});
      } else if (method === "GET" && path === "/api/projects") {
        response = json({ projects: [...this.projects.values()], total: this.projects.size });
      } else if (method === "GET" && getProject) {
        const project = this.projects.get(decodeURIComponent(getProject[1]!));
        response = project ? json(project) : json({ error: "project not found" }, 404);
      } else if (method === "GET" && taskRuns) {
        response = json(this.seedRuns());
      } else if (method === "GET" && taskDetail) {
        response = json({
          task: {
            ...this.runShape("r-1", "cancel_requested"),
            cancel_requested_at: "2026-10-03T10:00:00Z",
            cancel_requested_by_user_id: "u-1",
            issue_id: "i-child",
          },
          ancestors: [],
          descendants: [],
        });
      } else if (method === "POST" && taskCancel) {
        response = this.cancelRun(decodeURIComponent(taskCancel[2]!));
      } else if (method === "POST" && taskRetry) {
        response = json({
          id: "r-new-1",
          status: "queued",
          agent_id: "a-1",
          attempt: 1,
          created_at: "2026-10-03T10:05:00Z",
          rerun_of_task_id: null,
          retry_of_task_id: decodeURIComponent(taskRetry[2]!),
        });
      } else {
        response = json({ error: `fake backend: unrouted ${method} ${path}` }, 500);
      }
      return response;
    }) as typeof fetch;
  }

  private nextStamp(): string {
    this.clock += 1;
    return `2026-10-03T09:00:0${this.clock}Z`;
  }

  private casGuard(
    resource: { id: string; revision: number },
    resourceType: string,
    expected: unknown,
  ): Response | undefined {
    if (expected === undefined) return undefined;
    if (typeof expected !== "number" || !Number.isInteger(expected) || expected < 1) {
      return json({ error: "expected_revision must be a positive integer" }, 400);
    }
    if (resource.revision !== expected) {
      // Same body shape as the server's writeRevisionConflict.
      return json(
        {
          error: "resource changed since it was loaded",
          code: "revision_conflict",
          resource_type: resourceType,
          resource_id: resource.id,
          expected_revision: expected,
          actual_revision: resource.revision,
        },
        409,
      );
    }
    return undefined;
  }

  private updateIssue(id: string, body: Record<string, unknown>): Response {
    const issue = this.issues.get(id);
    if (!issue) return json({ error: "issue not found" }, 404);
    const guard = this.casGuard(issue, "issue", body.expected_revision);
    if (guard) return guard;
    const prevStatus = issue.status;
    const prevAssigned = issue.assignee_type !== null || issue.assignee_id !== null;
    for (const key of ["title", "description", "priority", "status"] as const) {
      if (body[key] !== undefined) (issue as unknown as Record<string, unknown>)[key] = body[key];
    }
    for (const key of NULLABLE_ISSUE_KEYS) {
      if (key in body) {
        (issue as unknown as Record<string, unknown>)[key] = body[key] ?? null;
      }
    }
    // Mirror the server's RUYI-275 disposition (issue.go: set when the write
    // would have enqueued a run and was suppressed; clear when it truly
    // starts a run or clears the assignee; keep otherwise) — a member
    // assign or metadata edit never flips the flag.
    const assigneeChanged = body.assignee_type !== undefined || body.assignee_id !== undefined;
    const statusChanged = body.status !== undefined && body.status !== prevStatus;
    const willEnqueue =
      (assigneeChanged && (body.assignee_type === "agent" || body.assignee_type === "squad")) ||
      statusChanged;
    const suppressedRun = willEnqueue && body.suppress_run === true;
    const assigneeCleared = prevAssigned && issue.assignee_type === null && issue.assignee_id === null;
    if (suppressedRun) issue.run_suppressed = true;
    else if (willEnqueue || assigneeCleared) issue.run_suppressed = false;
    issue.revision += 1;
    return json(issue);
  }

  private getRelations(id: string): Response {
    const issue = this.issues.get(id);
    if (!issue) return json({ error: "issue not found" }, 404);
    const ref = (other: FakeIssue) => ({
      id: other.id,
      identifier: other.identifier,
      title: other.title,
      status: other.status,
    });
    const parent = issue.parent_issue_id ? ref(this.issues.get(issue.parent_issue_id)!) : null;
    const blocks = [...this.blocks]
      .filter((edge) => edge.startsWith(`blocks:${id}:`))
      .map((edge) => ref(this.issues.get(edge.split(":")[2]!)!));
    const blockedBy = [...this.blocks]
      .filter((edge) => edge.endsWith(`:${id}`))
      .map((edge) => ref(this.issues.get(edge.split(":")[1]!)!));
    return json({
      issue_id: issue.id,
      identifier: issue.identifier,
      revision: issue.revision,
      parent,
      blocks,
      blocked_by: blockedBy,
      relates_to: [],
      supersedes: [],
      superseded_by: [],
    });
  }

  private addRelation(id: string, body: Record<string, unknown>): Response {
    const issue = this.issues.get(id);
    const target = this.issues.get(String(body.target_issue_id));
    if (!issue || !target) return json({ error: "issue not found" }, 404);
    const guard = this.casGuard(issue, "issue", body.expected_revision);
    if (guard) return guard;
    const type = String(body.type);
    const forward =
      type === "blocked_by" ? `blocks:${target.id}:${issue.id}` : `blocks:${issue.id}:${target.id}`;
    this.blocks.add(forward);
    issue.revision += 1;
    target.revision += 1;
    return json({
      added: true,
      relation: { id: forward, type, source_issue_id: issue.id, target_issue_id: target.id },
      issue: { id: issue.id, revision: issue.revision },
    });
  }

  private removeRelation(
    id: string,
    type: string,
    targetId: string,
    expected?: number,
  ): Response {
    const issue = this.issues.get(id);
    const target = this.issues.get(targetId);
    if (!issue || !target) return json({ error: "issue not found" }, 404);
    const guard = this.casGuard(issue, "issue", expected);
    if (guard) return guard;
    const forward =
      type === "blocked_by" ? `blocks:${targetId}:${id}` : `blocks:${id}:${targetId}`;
    if (!this.blocks.delete(forward)) {
      return json({ error: "relation_not_found: no such edge" }, 404);
    }
    issue.revision += 1;
    target.revision += 1;
    return json({
      removed: true,
      relation: { type, source_issue_id: id, target_issue_id: targetId },
      issue: { id, revision: issue.revision },
    });
  }

  private addComment(issueId: string, body: Record<string, unknown>): Response {
    if (!this.issues.has(issueId)) return json({ error: "issue not found" }, 404);
    this.commentSeq += 1;
    const id = `c-new-${this.commentSeq}`;
    const now = this.nextStamp();
    this.comments.set(id, {
      id,
      issue_id: issueId,
      content: String(body.content),
      revision: 1,
      created_at: now,
      updated_at: now,
      trigger_outcomes: [],
    });
    return json(this.comments.get(id));
  }

  private updateComment(id: string, body: Record<string, unknown>): Response {
    if (id === "c-403") {
      return json({ error: "only the author or a workspace admin can edit comments" }, 403);
    }
    if (id === "c-404") {
      return json({ error: "comment not found" }, 404);
    }
    if (id === "c-422") {
      return json(
        {
          error: "a mentioned agent cannot be invoked",
          code: "invalid_agent_mentions",
          invalid_mentions: [{ target_id: "a-ghost", reason_code: "not_dispatchable" }],
        },
        422,
      );
    }
    const comment = this.comments.get(id);
    if (!comment) return json({ error: "comment not found" }, 404);
    const guard = this.casGuard(comment, "comment", body.expected_revision);
    if (guard) return guard;
    comment.content = String(body.content);
    comment.revision += 1;
    comment.updated_at = this.nextStamp();
    comment.trigger_outcomes = [];
    return json(comment);
  }

  private deleteComment(id: string): Response {
    if (!this.comments.has(id)) return json({ error: "comment not found" }, 404);
    this.comments.delete(id);
    return new Response(undefined, { status: 204 });
  }

  private updateProject(id: string, body: Record<string, unknown>): Response {
    const project = this.projects.get(id);
    if (!project) return json({ error: "project not found" }, 404);
    const guard = this.casGuard(project, "project", body.expected_revision);
    if (guard) return guard;
    for (const key of ["title", "status", "priority"] as const) {
      if (body[key] !== undefined) (project as unknown as Record<string, unknown>)[key] = body[key];
    }
    for (const key of NULLABLE_PROJECT_KEYS) {
      if (key in body) {
        (project as unknown as Record<string, unknown>)[key] = body[key] ?? null;
      }
    }
    project.revision += 1;
    project.updated_at = this.nextStamp();
    return json(project);
  }

  private runShape(id: string, status: string): Record<string, unknown> {
    return {
      id,
      status,
      agent_id: "a-1",
      attempt: 1,
      created_at: "2026-10-03T09:30:00Z",
      started_at: null,
      completed_at: null,
      trigger_comment_id: id === "r-1" ? "c-1" : undefined,
      autopilot_run_id: id === "r-2" ? "ap-1" : undefined,
    };
  }

  private seedRuns(): Array<Record<string, unknown>> {
    return [this.runShape("r-1", "queued"), this.runShape("r-2", "completed")];
  }

  private cancelRun(runId: string): Response {
    if (runId === "r-done") {
      return json({ error: "run already finished", code: "not_cancellable" }, 409);
    }
    return json({
      code: "cancelled",
      message: "queued run cancelled",
      task: { ...this.runShape(runId, "cancelled"), cancel_requested_at: null },
    });
  }
}

// ---- helpers ---------------------------------------------------------------

function json(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function silentLogger(): Logger {
  return { info: () => undefined, error: () => undefined };
}

function textOf(result: unknown): string {
  const content = (result as { content?: Array<{ type: string; text?: string }> }).content;
  return content?.[0]?.text ?? "";
}

function propertiesOf(name: string): Record<
  string,
  { type?: string | string[]; minimum?: number; description?: string }
> {
  const tool = findTool(name);
  expect(tool, `${name} must be registered`).toBeDefined();
  const schema = tool?.inputSchema as { properties?: Record<string, unknown> };
  return (schema.properties ?? {}) as Record<
    string,
    { type?: string | string[]; minimum?: number; description?: string }
  >;
}

async function callHandlerJson(
  client: MulticaClient,
  name: string,
  args: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  return (await findTool(name)?.handler(args, client)) as Record<string, unknown>;
}

async function callToolJson(
  client: Client,
  name: string,
  args: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  const result = await client.callTool({ name, arguments: args });
  expect(result.isError, `${name} should succeed: ${textOf(result)}`).toBeFalsy();
  return JSON.parse(textOf(result)) as Record<string, unknown>;
}

async function connectViaMcp(
  backend: FakeRestBackend,
): Promise<{ client: Client; cleanup: () => Promise<void> }> {
  const server = createMcpServer(backend.client());
  const mcpClient = new Client({ name: "vitest", version: "0.0.0" });
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await server.connect(serverTransport);
  await mcpClient.connect(clientTransport);
  return {
    client: mcpClient,
    cleanup: async () => {
      await mcpClient.close();
      await server.close();
    },
  };
}
