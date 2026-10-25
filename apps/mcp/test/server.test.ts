import { afterEach, describe, expect, it } from "vitest";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";

import type { Logger } from "../src/log.js";
import { MulticaClient } from "../src/rest.js";
import { createMcpServer, SERVER_NAME } from "../src/server.js";
import { TOOL_RISK_MATRIX } from "../src/tool-risk.js";

const TOKEN = `mul_${"a1b2c3d4e5".repeat(4)}`;

function silentLogger(): Logger {
  return { info: () => undefined, error: () => undefined };
}

interface Fixture {
  client: Client;
  cleanup: () => Promise<void>;
}

async function connectServer(
  backendHandler: (pathname: string, input?: RequestInfo | URL, init?: RequestInit) => unknown | Promise<unknown>,
  backendStatus = 200,
  mcpInstanceId?: string,
): Promise<Fixture> {
  const fetchImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const pathname = new URL(String(input)).pathname;
    const body = await backendHandler(pathname, input, init);
    return new Response(JSON.stringify(body), {
      status: backendStatus,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  const restClient = new MulticaClient({
    serverUrl: "https://api.example.com",
    token: TOKEN,
    fetchImpl,
    logger: silentLogger(),
    auditServiceCredential: "private-audit-credential",
    ...(mcpInstanceId ? { mcpInstanceId, fileAccessInstanceCredential: "private-instance-credential" } : {}),
  });
  const server = createMcpServer(restClient);
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

describe("createMcpServer (via in-memory client)", () => {
  const fixtures: Array<() => Promise<void>> = [];
  afterEach(async () => {
    while (fixtures.length > 0) {
      const cleanup = fixtures.pop();
      await cleanup?.();
    }
  });

  it("exposes the multica-mcp identity and instructions", async () => {
    const { client, cleanup } = await connectServer(async () => []);
    fixtures.push(cleanup);
    expect(client.getServerVersion()?.name).toBe(SERVER_NAME);
    expect(client.getInstructions()).toContain("Multica");
  });

  it("list_tools returns the full v1 surface with object schemas", async () => {
    const { client, cleanup } = await connectServer(async () => []);
    fixtures.push(cleanup);
    const { tools } = await client.listTools();
    const names = tools.map((tool) => tool.name).sort();
    expect(names).toEqual([
      "add_comment",
      "answer_decisions",
      "apply_execution_profile",
      "archive_agent",
      "archive_all_inbox",
      "archive_all_read_inbox",
      "archive_completed_inbox",
      "archive_inbox_item",
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
      "create_prompt_proposal",
      "create_quick_reply",
      "create_squad",
      "delete_comment",
      "delete_execution_profile",
      "delete_project_resource",
      "delete_quick_reply",
      "direct_revise_clause",
      "dispatch_agent",
      "edit_comment",
      "get_agent",
      "get_agent_runtime_config",
      "get_autopilot",
      "get_autopilot_run",
      "get_autopilot_run_diagnostics",
      "get_clause",
      "get_clause_reconciliation",
      "get_comment",
      "get_daemon_instance",
      "get_decision",
      "get_execution_profile",
      "get_execution_topology",
      "get_file_metadata",
      "get_inbox_unread_count",
      "get_inbox_unread_summary",
      "get_issue",
      "get_issue_relations",
      "get_project",
      "get_prompt_versions",
      "get_run",
      "get_run_activity",
      "get_runtime",
      "get_runtime_models",
      "get_squad",
      "list_agents",
      "list_archived_inbox",
      "list_autopilot_runs",
      "list_clauses",
      "list_comments",
      "list_daemon_instances",
      "list_decisions",
      "list_execution_profiles",
      "list_files",
      "list_inbox",
      "list_issue_runs",
      "list_issues",
      "list_project_resources",
      "list_projects",
      "list_prompt_proposals",
      "list_quick_replies",
      "list_runs",
      "list_runtimes",
      "list_squads",
      "list_workspaces",
      "manage_issue_relations",
      "mark_all_inbox_read",
      "mark_inbox_item_read",
      "mark_inbox_item_unread",
      "preview_clause_revision",
      "preview_prompt_proposal",
      "progress_digest",
      "read_file_chunk",
      "restore_agent",
      "retry_run",
      "rework_prompt_proposal",
      "search_audit_events",
      "search_clauses",
      "search_issues",
      "submit_prompt_proposal",
      "trigger_autopilot",
      "unarchive_inbox_item",
      "update_agent",
      "update_agent_runtime_config",
      "update_execution_profile",
      "update_issue",
      "update_issue_status",
      "update_project",
      "update_project_resource",
      "update_prompt_proposal",
      "update_quick_reply",
      "update_squad",
    ]);
    for (const tool of tools) {
      expect(tool.inputSchema).toMatchObject({ type: "object" });
    }
  });

  it("exposes exhaustive, explicit risk annotations over the MCP protocol", async () => {
    const { client, cleanup } = await connectServer(async () => []);
    fixtures.push(cleanup);
    const { tools } = await client.listTools();
    const names = tools.map((tool) => tool.name).sort();
    expect(names).toEqual(Object.keys(TOOL_RISK_MATRIX).sort());

    for (const tool of tools) {
      expect(tool.annotations, tool.name).toEqual(TOOL_RISK_MATRIX[tool.name]?.annotations);
      expect(tool.annotations, tool.name).toMatchObject({
        readOnlyHint: expect.any(Boolean),
        destructiveHint: expect.any(Boolean),
        idempotentHint: expect.any(Boolean),
        openWorldHint: expect.any(Boolean),
      });
    }
    expect(tools.find((tool) => tool.name === "list_quick_replies")?.annotations?.readOnlyHint).toBe(true);
    expect(tools.find((tool) => tool.name === "get_runtime_models")?.annotations).toEqual({
      readOnlyHint: false,
      destructiveHint: false,
      idempotentHint: false,
      openWorldHint: true,
    });
    expect(tools.find((tool) => tool.name === "add_comment")?.annotations?.openWorldHint).toBe(true);
    expect(tools.find((tool) => tool.name === "answer_decisions")?.annotations?.openWorldHint).toBe(true);
  });

  it("tools/call returns pretty JSON text for successful calls", async () => {
    const { client, cleanup } = await connectServer(async () => [
      { id: "w1", slug: "ws", name: "WS" },
    ]);
    fixtures.push(cleanup);
    const result = await client.callTool({ name: "list_workspaces", arguments: {} });
    expect(result.isError).toBeFalsy();
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    const parsed = JSON.parse(text) as { total: number; workspaces: Array<{ slug: string }> };
    expect(parsed.total).toBe(1);
    expect(parsed.workspaces[0]?.slug).toBe("ws");
  });

  it("audits inbox calls through the existing endpoints without recording inbox content", async () => {
    const calls: Array<{ path: string; body: string; headers: Record<string, string> }> = [];
    const item = {
      id: "0b7f4c1e-1111-4222-8333-abcdefabcdef",
      issue_id: null,
      type: "autopilot_paused",
      body: "private notification body",
    };
    const fixture = await connectServer(async (pathname, _input, init) => {
      calls.push({
        path: pathname,
        body: String(init?.body ?? ""),
        headers: init?.headers as Record<string, string>,
      });
      if (pathname === "/api/inbox") return [item];
      return { status: "recorded" };
    });
    fixtures.push(fixture.cleanup);

    const result = await fixture.client.callTool({
      name: "list_inbox",
      arguments: { workspace: "0b7f4c1e-1111-4222-8333-abcdefabcdef" },
    });
    expect(result.isError).toBeFalsy();
    expect(calls.map((call) => call.path)).toEqual([
      "/api/mcp/audit-start",
      "/api/inbox",
      "/api/mcp/audit-finish",
    ]);
    const auditBodies = `${calls[0]?.body}\n${calls[2]?.body}`;
    expect(auditBodies).toContain("list_inbox");
    expect(JSON.parse(calls[0]?.body ?? "{}")).toMatchObject({
      reported_client_name: "vitest",
      reported_client_version: "0.0.0",
    });
    expect(auditBodies).not.toContain("private notification body");
    expect(auditBodies).not.toContain(TOKEN);
    expect(calls[0]?.headers["X-Multica-MCP-Service-Credential"]).toBe("private-audit-credential");
    expect(calls[1]?.headers["X-Multica-MCP-Service-Credential"]).toBeUndefined();
  });

  it("does not perform an inbox operation when audit start fails", async () => {
    const paths: string[] = [];
    const fixture = await connectServer(async (pathname) => {
      paths.push(pathname);
      return { error: "audit unavailable" };
    }, 503);
    fixtures.push(fixture.cleanup);

    const result = await fixture.client.callTool({
      name: "list_inbox",
      arguments: { workspace: "0b7f4c1e-1111-4222-8333-abcdefabcdef" },
    });

    expect(result.isError).toBe(true);
    expect(paths).toEqual(["/api/mcp/audit-start"]);
  });

  it("classifies rejected file operations as refusals and exposes queued finish audit", async () => {
    const calls: Array<{ path: string; body: string }> = [];
    const fixture = await connectServer(async (pathname, _input, init) => {
      calls.push({ path: pathname, body: String(init?.body ?? "") });
      if (pathname === "/api/file-access/metadata") return { operation_id: "op-1", status: "pending" };
      if (pathname === "/api/file-access/operations/op-1") {
        return { operation_id: "op-1", kind: "metadata", status: "rejected", result: null, error: "permission_denied" };
      }
      if (pathname === "/api/mcp/audit-finish") return { status: "queued" };
      return { status: "recorded" };
    }, 200, "mcp-test-instance");
    fixtures.push(fixture.cleanup);

    const result = await fixture.client.callTool({
      name: "get_file_metadata",
      arguments: { workspace: "0b7f4c1e-1111-4222-8333-abcdefabcdef", path: "/srv/data/a.txt" },
    });

    expect(result.isError).toBe(true);
    expect(JSON.stringify(result)).toContain("audit_status: queued");
    const finish = JSON.parse(calls.find((call) => call.path === "/api/mcp/audit-finish")?.body ?? "{}");
    expect(finish, JSON.stringify(calls)).toMatchObject({ status: "refused", error_code: "permission_denied", operation_id: "op-1" });
  });

  it("tools/call maps backend errors to isError results without leaking the token", async () => {
    const { client, cleanup } = await connectServer(async () => ({ error: "boom" }), 500);
    fixtures.push(cleanup);
    const result = await client.callTool({ name: "list_workspaces", arguments: {} });
    expect(result.isError).toBe(true);
    const text = JSON.stringify(result);
    expect(text).toContain("boom");
    expect(text).not.toContain(TOKEN);
  });

  it("tools/call reports unknown tools as errors", async () => {
    const { client, cleanup } = await connectServer(async () => []);
    fixtures.push(cleanup);
    const result = await client.callTool({
      name: "delete_everything",
      arguments: {},
    });
    expect(result.isError).toBe(true);
    expect(JSON.stringify(result)).toContain("Unknown tool");
  });

  it("tools/call surfaces validation failures as error results", async () => {
    const { client, cleanup } = await connectServer(async () => []);
    fixtures.push(cleanup);
    const result = await client.callTool({
      name: "create_issue",
      arguments: { workspace: "ws" },
    });
    expect(result.isError).toBe(true);
    expect(JSON.stringify(result)).toContain("'title' is required");
  });
});
