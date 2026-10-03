/**
 * Combined regression for the RUYI-350 (update_issue) / RUYI-351 (relations)
 * / RUYI-353 (bulk_update_issues) union on final main (RUYI-399).
 *
 * The three features repeatedly merge-conflicted in apps/mcp. Per-tool tests
 * pin each tool in isolation; this file pins the CROSS-tool contracts the
 * conflict resolutions could have broken:
 *   - the four tools coexist in tools/list with correct annotations;
 *   - every expected_revision input shares one floor (the server rejects <1);
 *   - the three parent write paths (update_issue, bulk item,
 *     manage_issue_relations set/clear_parent) hit one wire contract;
 *   - run side effects stay structurally impossible on the metadata-only
 *     paths and stay suppressible on the bulk path;
 *   - one server 409 maps to each tool's documented outcome shape.
 *
 * The FakeRestBackend mirrors the Go handlers' observable contract
 * (PATCH-by-rawFields, positive-integer CAS, double endpoint revision bump,
 * symmetric relation views) — server/internal/handler/issue.go and
 * issue_relation.go are the sources of truth it was written against.
 */

import { describe, expect, it } from "vitest";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";

import type { Logger } from "../src/log.js";
import { MulticaClient } from "../src/rest.js";
import { findTool, TOOL_DEFINITIONS } from "../src/tools.js";

const WS = "voice-notes";
const FOUR_TOOLS = [
  "update_issue",
  "get_issue_relations",
  "manage_issue_relations",
  "bulk_update_issues",
] as const;

function schemaOf(name: string): Record<string, unknown> {
  const tool = findTool(name);
  expect(tool, `${name} must be registered`).toBeDefined();
  return tool?.inputSchema as Record<string, unknown>;
}

function propertiesOf(name: string): Record<
  string,
  { type?: string | string[]; minimum?: number; description?: string }
> {
  return (schemaOf(name).properties ?? {}) as Record<
    string,
    { type?: string | string[]; minimum?: number; description?: string }
  >;
}

// ---- Part A: registration surface --------------------------------------

describe("four-tool registration union (RUYI-399)", () => {
  it("registers each of the four tools exactly once (no duplicates, no shadowing)", () => {
    const names = TOOL_DEFINITIONS.map((tool) => tool.name);
    expect(new Set(names).size).toBe(names.length);
    for (const name of FOUR_TOOLS) {
      expect(names.filter((candidate) => candidate === name)).toHaveLength(1);
      expect(findTool(name)?.name).toBe(name);
    }
  });

  it("exposes the four tools over tools/list with the correct readOnlyHint split", async () => {
    const { client, cleanup } = await connectViaMcp(new FakeRestBackend());
    try {
      const { tools } = await client.listTools();
      for (const name of FOUR_TOOLS) {
        const hits = tools.filter((tool) => tool.name === name);
        expect(hits).toHaveLength(1);
        expect(hits[0]?.inputSchema).toMatchObject({ type: "object" });
      }
      // get_issue_relations is read-only; the three writers are not.
      const hint = Object.fromEntries(
        tools.map((tool) => [tool.name, tool.annotations?.readOnlyHint === true]),
      );
      expect(hint.get_issue_relations).toBe(true);
      expect(hint.update_issue).toBe(false);
      expect(hint.manage_issue_relations).toBe(false);
      expect(hint.bulk_update_issues).toBe(false);
    } finally {
      await cleanup();
    }
  });

  it("keeps one expected_revision floor across every writer: schema minimum 1", () => {
    // The server rejects expected_revision < 1 with a bare 400
    // (issue.go: "expected_revision must be a positive integer"); every
    // client-side schema must gate at the same floor so callers get a
    // ToolInputError instead of an opaque round-trip failure.
    // get_issue_relations is read-only and takes no lock at all.
    expect(propertiesOf("get_issue_relations").expected_revision).toBeUndefined();
    for (const name of ["update_issue", "update_issue_status", "assign_issue", "manage_issue_relations"]) {
      const props = propertiesOf(name);
      expect(props.expected_revision?.minimum, `${name}.expected_revision`).toBe(1);
    }
    const bulkItemProps = propertiesOf("bulk_update_issues").updates as {
      items: { properties: Record<string, { minimum?: number }> };
    };
    expect(bulkItemProps.items.properties.expected_revision?.minimum).toBe(1);
  });

  it("keeps the run-side-effect boundary structural: metadata-only tools cannot carry run triggers", () => {
    // update_issue and manage_issue_relations promise "never triggers a run"
    // in prose; the schema makes it structural — no status or assignee keys
    // exist to send. bulk_update_issues DOES carry them and therefore must
    // carry suppress_run.
    for (const name of ["update_issue", "manage_issue_relations"] as const) {
      const props = propertiesOf(name);
      expect(props.status, `${name}.status`).toBeUndefined();
      expect(props.assignee_type, `${name}.assignee_type`).toBeUndefined();
      expect(props.assignee_id, `${name}.assignee_id`).toBeUndefined();
      expect(props.suppress_run, `${name}.suppress_run`).toBeUndefined();
    }
    const bulk = propertiesOf("bulk_update_issues");
    const itemProps = (bulk.updates as {
      items: { properties: Record<string, unknown> };
    }).items.properties;
    expect(itemProps.status).toBeDefined();
    expect(itemProps.assignee_type).toBeDefined();
    expect(itemProps.suppress_run).toBeDefined();
    expect(bulk.suppress_run).toBeDefined();
  });
});

// ---- Part B: shared wire contract --------------------------------------

describe("parent write paths agree on one wire contract (RUYI-399)", () => {
  it("update_issue, bulk item and set_parent all PUT the same body for the same re-parent", async () => {
    const bodies: Array<Record<string, unknown>> = [];
    const backend = new FakeRestBackend();
    const client = backend.client((raw) => bodies.push(raw));

    const updateIssue = findTool("update_issue");
    await updateIssue?.handler(
      { workspace: WS, issue: "i-child", parent_issue_id: "i-parent" },
      client,
    );
    const bulk = findTool("bulk_update_issues");
    await bulk?.handler(
      { workspace: WS, updates: [{ issue: "i-child", parent_issue_id: "i-parent" }] },
      client,
    );
    const manage = findTool("manage_issue_relations");
    await manage?.handler(
      { workspace: WS, issue: "i-child", action: "set_parent", target_issue: "i-parent" },
      client,
    );

    expect(bodies).toHaveLength(3);
    for (const body of bodies) {
      expect(body).toEqual({ parent_issue_id: "i-parent" });
    }
  });

  it("clear_parent and update_issue send the same explicit-null clear; bulk items reject null", async () => {
    const bodies: Array<Record<string, unknown>> = [];
    const backend = new FakeRestBackend();
    const client = backend.client((raw) => bodies.push(raw));

    const updateIssue = findTool("update_issue");
    await updateIssue?.handler(
      { workspace: WS, issue: "i-child", parent_issue_id: null },
      client,
    );
    const manage = findTool("manage_issue_relations");
    await manage?.handler({ workspace: WS, issue: "i-child", action: "clear_parent" }, client);

    expect(bodies).toHaveLength(2);
    expect(bodies[0]).toEqual({ parent_issue_id: null });
    expect(bodies[1]).toEqual({ parent_issue_id: null });

    // Bulk items type parent as a plain string (no clear): the item builder
    // drops a null, so a null-only item is rejected as writing nothing rather
    // than silently widening into a keep, and mixed with a real field the
    // null key never reaches the wire.
    const bulk = findTool("bulk_update_issues");
    await expect(
      bulk?.handler(
        { workspace: WS, updates: [{ issue: "i-child", parent_issue_id: null as unknown as string }] },
        client,
      ),
    ).rejects.toThrow(/must set at least one writable field/);
    await bulk?.handler(
      { workspace: WS, updates: [{ issue: "i-child", parent_issue_id: null as unknown as string, status: "done" }] },
      client,
    );
    expect(bodies[2]).toEqual({ status: "done" });
    // Pin the schema side of the same split: bulk items type parent as a
    // plain string, update_issue as string|null.
    const bulkItemProps = propertiesOf("bulk_update_issues").updates as {
      items: { properties: Record<string, { type?: string | string[] }> };
    };
    expect(bulkItemProps.items.properties.parent_issue_id?.type).toBe("string");
    expect(propertiesOf("update_issue").parent_issue_id?.type).toContain("null");
  });
});

// ---- Part C: stateful end-to-end union scenarios ------------------------

describe("combined scenarios over the MCP wire (RUYI-399)", () => {
  it("update_issue → relations view → bulk → manage_issue_relations keeps one consistent state", async () => {
    const backend = new FakeRestBackend();
    const { client, cleanup } = await connectViaMcp(backend);
    try {
      // 1. update_issue: metadata edit with a fresh revision.
      const updated = await callToolJson(client, "update_issue", {
        workspace: WS,
        issue: "i-child",
        title: "Renamed via update_issue",
        parent_issue_id: "i-parent",
        expected_revision: 1,
      });
      expect(updated.updated).toBe(true);
      expect(updated.revision).toBe(2);

      // 2. get_issue_relations reflects the new parent (same state, read side).
      const relations = await callToolJson(client, "get_issue_relations", {
        workspace: WS,
        issue: "i-child",
      });
      expect(relations.parent).toMatchObject({ id: "i-parent", identifier: "VOI-9" });
      expect(relations.revision).toBe(2);

      // 3. bulk continues past a stale-revision item and applies the rest;
      //    the applied item's revision bump is not rolled back.
      const bulk = await callToolJson(client, "bulk_update_issues", {
        workspace: WS,
        on_error: "continue",
        updates: [
          { issue: "i-child", expected_revision: 99, priority: "low" },
          { issue: "i-child", priority: "high" },
          { issue: "i-other", status: "in_progress", suppress_run: true },
        ],
      });
      expect(bulk.total).toBe(3);
      expect(bulk.updated).toBe(2);
      expect(bulk.failed).toBe(1);
      const bulkResults = bulk.results as Array<Record<string, unknown>>;
      expect(bulkResults[0]).toMatchObject({ outcome: "failed", error: { code: "conflict" } });
      expect(bulkResults[1]).toMatchObject({ outcome: "updated", revision: 3 });
      expect(bulkResults[2]).toMatchObject({
        outcome: "updated",
        status: "in_progress",
        run_suppressed: true,
      });

      // 4. manage_issue_relations adds a blocks edge; both endpoints bump and
      //    both relation views stay symmetric.
      const added = await callToolJson(client, "manage_issue_relations", {
        workspace: WS,
        issue: "i-child",
        action: "add_relation",
        relation_type: "blocks",
        target_issue: "i-other",
        expected_revision: 3,
      });
      expect(added.updated).toBe(true);
      expect(added.revision).toBe(4);
      const childView = await callToolJson(client, "get_issue_relations", {
        workspace: WS,
        issue: "i-child",
      });
      const otherView = await callToolJson(client, "get_issue_relations", {
        workspace: WS,
        issue: "i-other",
      });
      expect(childView.blocks).toEqual([expect.objectContaining({ id: "i-other" })]);
      expect(otherView.blocked_by).toEqual([expect.objectContaining({ id: "i-child" })]);
      // i-other: seed 1 → bulk status write 2 → edge bump on both endpoints 3.
      expect(otherView.revision).toBe(3);

      // 5. clear_parent through the third path; the parent view clears while
      //    the blocks edge survives — one state, two tools' writes.
      const cleared = await callToolJson(client, "manage_issue_relations", {
        workspace: WS,
        issue: "i-child",
        action: "clear_parent",
      });
      expect(cleared.revision).toBe(5);
      const afterClear = await callToolJson(client, "get_issue_relations", {
        workspace: WS,
        issue: "i-child",
      });
      expect(afterClear.parent).toBeNull();
      expect(afterClear.blocks).toHaveLength(1);
    } finally {
      await cleanup();
    }
  });

  it("CAS: stale revisions fail per tool with no partial write, fresh retries succeed", async () => {
    const backend = new FakeRestBackend();
    const { client, cleanup } = await connectViaMcp(backend);
    try {
      // update_issue surfaces the structured conflict outcome...
      const conflict = await client.callTool({
        name: "update_issue",
        arguments: { workspace: WS, issue: "i-child", title: "lost race", expected_revision: 41 },
      });
      expect(conflict.isError).toBeFalsy();
      const parsed = JSON.parse(textOf(conflict)) as Record<string, unknown>;
      expect(parsed).toMatchObject({ updated: false, code: "revision_conflict" });
      expect(backend.issues.get("i-child")?.title).toBe("Child"); // nothing written

      // ...and the same 409 comes back as an error result on the relation
      // path, with the anchor issue untouched.
      const relationConflict = await client.callTool({
        name: "manage_issue_relations",
        arguments: {
          workspace: WS,
          issue: "i-child",
          action: "set_parent",
          target_issue: "i-parent",
          expected_revision: 41,
        },
      });
      expect(relationConflict.isError).toBe(true);
      expect(textOf(relationConflict)).toContain("revision_conflict");
      expect(backend.issues.get("i-child")?.parent_issue_id).toBeNull();

      // A fresh revision lands through both paths and keeps advancing.
      const reparented = await callToolJson(client, "manage_issue_relations", {
        workspace: WS,
        issue: "i-child",
        action: "set_parent",
        target_issue: "i-parent",
        expected_revision: 1,
      });
      expect(reparented.revision).toBe(2);
      const retitled = await callToolJson(client, "update_issue", {
        workspace: WS,
        issue: "i-child",
        title: "won race",
        expected_revision: 2,
      });
      expect(retitled.revision).toBe(3);
    } finally {
      await cleanup();
    }
  });

  it("run-side effects: metadata/relation writes never carry run triggers, bulk respects suppress_run", async () => {
    const backend = new FakeRestBackend();
    const { client, cleanup } = await connectViaMcp(backend);
    try {
      await callToolJson(client, "update_issue", {
        workspace: WS,
        issue: "i-child",
        title: "metadata only",
        due_date: null,
      });
      await callToolJson(client, "manage_issue_relations", {
        workspace: WS,
        issue: "i-child",
        action: "add_relation",
        relation_type: "relates_to",
        target_issue: "i-other",
      });
      // No write on either path ever carried a run-triggering key.
      for (const body of backend.putBodies) {
        expect(body.status).toBeUndefined();
        expect(body.assignee_type).toBeUndefined();
        expect(body.assignee_id).toBeUndefined();
      }

      // bulk CAN trigger runs (status change), so it must honor suppress_run
      // at both levels and echo run_suppressed back.
      await callToolJson(client, "bulk_update_issues", {
        workspace: WS,
        suppress_run: true,
        updates: [
          { issue: "i-child", status: "in_progress" },
          { issue: "i-other", status: "done", suppress_run: false },
        ],
      });
      expect(backend.putBodies.at(-2)).toMatchObject({ status: "in_progress", suppress_run: true });
      expect(backend.putBodies.at(-1)).toMatchObject({ status: "done", suppress_run: false });
    } finally {
      await cleanup();
    }
  });
});

// ---- Fake REST backend (server contract mirror) -------------------------

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

const NULLABLE_PATCH_KEYS = [
  "project_id",
  "start_date",
  "due_date",
  "parent_issue_id",
  "assignee_type",
  "assignee_id",
] as const;

/**
 * Minimal in-memory mirror of the Go handlers' observable contract for the
 * four tools: PATCH-by-key-presence (explicit null clears), positive-integer
 * expected_revision CAS answering the structured revision_conflict body, a
 * revision bump on every committed write, and relation edges visible from
 * both endpoints. Deliberately no cycle detection or permission model —
 * those are server-side concerns outside this regression's scope.
 */
class FakeRestBackend {
  issues = new Map<string, FakeIssue>();
  blocks = new Set<string>(); // forward edges "blocks:<source>:<target>"
  putBodies: Array<Record<string, unknown>> = [];

  constructor() {
    this.issues.set("i-child", this.seed("i-child", "VOI-1", "Child"));
    this.issues.set("i-parent", this.seed("i-parent", "VOI-9", "Parent"));
    this.issues.set("i-other", this.seed("i-other", "VOI-2", "Other"));
  }

  private seed(id: string, identifier: string, title: string): FakeIssue {
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

  /** A real MulticaClient wired to this backend, with a wire-body audit tap. */
  client(onRequestBody?: (body: Record<string, unknown>) => void): MulticaClient {
    return new MulticaClient({
      serverUrl: "https://api.example.com",
      token: "mul_test",
      fetchImpl: this.fetchImpl(onRequestBody),
      logger: silentLogger(),
    });
  }

  private fetchImpl(
    onRequestBody?: (body: Record<string, unknown>) => void,
  ): typeof fetch {
    return (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input));
      const method = init?.method ?? "GET";
      const path = url.pathname;
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : {};

      const putIssue = path.match(/^\/api\/issues\/([^/]+)$/);
      const relations = path.match(/^\/api\/issues\/([^/]+)\/relations$/);
      const relation = path.match(/^\/api\/issues\/([^/]+)\/relations\/([^/]+)\/([^/]+)$/);

      let response: Response;
      if (method === "PUT" && putIssue) {
        this.putBodies.push(body);
        onRequestBody?.(body);
        response = this.updateIssue(decodeURIComponent(putIssue[1]!), body);
      } else if (method === "GET" && relations) {
        response = this.getRelations(decodeURIComponent(relations[1]!));
      } else if (method === "POST" && relations) {
        response = this.addRelation(decodeURIComponent(relations[1]!), body);
      } else if (method === "DELETE" && relation) {
        const expected = url.searchParams.get("expected_revision");
        response = this.removeRelation(
          decodeURIComponent(relation[1]!),
          decodeURIComponent(relation[2]!),
          decodeURIComponent(relation[3]!),
          expected === null ? undefined : Number(expected),
        );
      } else {
        response = json({ error: `fake backend: unrouted ${method} ${path}` }, 500);
      }
      return response;
    }) as typeof fetch;
  }

  private casGuard(issue: FakeIssue, expected: unknown): Response | undefined {
    if (expected === undefined) return undefined;
    if (typeof expected !== "number" || !Number.isInteger(expected) || expected < 1) {
      return json({ error: "expected_revision must be a positive integer" }, 400);
    }
    if (issue.revision !== expected) {
      // Same body shape as handler.writeRevisionConflict.
      return json(
        {
          error: "resource changed since it was loaded",
          code: "revision_conflict",
          resource_type: "issue",
          resource_id: issue.id,
          expected_revision: expected,
          actual_revision: issue.revision,
        },
        409,
      );
    }
    return undefined;
  }

  private updateIssue(id: string, body: Record<string, unknown>): Response {
    const issue = this.issues.get(id);
    if (!issue) return json({ error: "issue not found" }, 404);
    const guard = this.casGuard(issue, body.expected_revision);
    if (guard) return guard;
    for (const key of ["title", "description", "priority", "status"] as const) {
      if (body[key] !== undefined) (issue as unknown as Record<string, unknown>)[key] = body[key];
    }
    for (const key of NULLABLE_PATCH_KEYS) {
      if (key in body) {
        (issue as unknown as Record<string, unknown>)[key] = body[key] ?? null;
      }
    }
    if (body.suppress_run === true) issue.run_suppressed = true;
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
    const parent = issue.parent_issue_id
      ? ref(this.issues.get(issue.parent_issue_id)!)
      : null;
    const blocks = [...this.blocks]
      .filter((edge) => edge === `blocks:${id}` || edge.startsWith(`blocks:${id}:`))
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
    const guard = this.casGuard(issue, body.expected_revision);
    if (guard) return guard;
    const type = String(body.type);
    // Caller-frame types normalize to a forward row, like the server.
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
    const guard = this.casGuard(issue, expected);
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
}

// ---- helpers ------------------------------------------------------------

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
  const server = createMcpServerForTest(backend);
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

// Re-imported here instead of at the top to keep the fake-backend section
// self-contained between the tests and their helpers.
import { createMcpServer } from "../src/server.js";

function createMcpServerForTest(backend: FakeRestBackend) {
  return createMcpServer(backend.client());
}
