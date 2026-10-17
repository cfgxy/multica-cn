import { describe, expect, it } from "vitest";

import { MulticaClient } from "../src/rest.js";
import { findTool } from "../src/tools.js";

const WS = "voice-notes";
const ITEM = "0b7f4c1e-1111-4222-8333-abcdefabcdef";
const TOKEN = `mul_${"a1b2c3d4e5".repeat(4)}`;

describe("inbox REST to MCP mapping", () => {
  it("preserves the full issue-less system notification projection", async () => {
    const systemNotice = {
      id: ITEM,
      workspace_id: "ws-id",
      recipient_type: "member",
      recipient_id: "user-id",
      type: "autopilot_paused",
      severity: "attention",
      issue_id: null,
      title: "Automation paused",
      body: "A system notice body",
      read: false,
      archived: false,
      created_at: "2026-10-17T00:00:00Z",
      issue_status: null,
      issue_priority: null,
      issue_identifier: null,
      actor_type: "system",
      actor_id: null,
      details: { reason: "paused" },
    };
    const issueNotice = {
      ...systemNotice,
      id: "1c8d5a7b-2222-4333-8444-abcdefabcdef",
      type: "issue_assigned",
      issue_id: "2d9e6b8c-3333-4444-8555-abcdefabcdef",
      issue_identifier: "VOI-42",
      issue_status: "todo",
      issue_priority: "high",
    };
    const tool = findTool("list_inbox");
    const result = await tool?.handler({ workspace: WS }, {
      listInbox: async () => [systemNotice, issueNotice],
    } as unknown as MulticaClient);
    expect(result).toEqual([systemNotice, issueNotice]);
    expect((result as typeof systemNotice[])[0]?.issue_id).toBeNull();
    expect((result as typeof systemNotice[])[0]?.details).toEqual({ reason: "paused" });
    expect((result as typeof issueNotice[])[1]?.issue_id).toBe(issueNotice.issue_id);
    expect((result as typeof issueNotice[])[1]?.issue_identifier).toBe("VOI-42");
  });

  it("maps every existing inbox route without adding query contracts", async () => {
    const calls: Array<{ method: string; path: string; body?: string }> = [];
    const fetchImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof URL ? input : new URL(String(input));
      calls.push({ method: init?.method ?? "GET", path: url.pathname, body: init?.body as string | undefined });
      return new Response(JSON.stringify(url.pathname.includes("/read") || url.pathname.endsWith("/archive") || url.pathname.endsWith("/unarchive")
        ? { id: ITEM, issue_id: null }
        : url.pathname.endsWith("unread-count") || url.pathname.includes("archive-") || url.pathname.endsWith("mark-all-read")
          ? { count: 2 }
          : []), { status: 200, headers: { "Content-Type": "application/json" } });
    }) as typeof fetch;
    const client = new MulticaClient({ serverUrl: "https://api.example.com", token: TOKEN, fetchImpl });

    const tools: Array<[string, Record<string, unknown>]> = [
      ["list_inbox", { workspace: WS }],
      ["list_archived_inbox", { workspace: WS }],
      ["get_inbox_unread_count", { workspace: WS }],
      ["get_inbox_unread_summary", { workspace: WS }],
      ["mark_all_inbox_read", { workspace: WS }],
      ["archive_all_inbox", { workspace: WS }],
      ["archive_all_read_inbox", { workspace: WS }],
      ["archive_completed_inbox", { workspace: WS }],
      ["mark_inbox_item_read", { workspace: WS, inbox_item_id: ITEM }],
      ["mark_inbox_item_unread", { workspace: WS, inbox_item_id: ITEM }],
      ["archive_inbox_item", { workspace: WS, inbox_item_id: ITEM }],
      ["unarchive_inbox_item", { workspace: WS, inbox_item_id: ITEM }],
    ];
    for (const [name, args] of tools) {
      const tool = findTool(name);
      expect(tool, name).toBeDefined();
      await tool?.handler(args, client);
    }

    expect(calls.map(({ method, path }) => [method, path])).toEqual([
      ["GET", "/api/inbox"],
      ["GET", "/api/inbox/archived"],
      ["GET", "/api/inbox/unread-count"],
      ["GET", "/api/inbox/unread-summary"],
      ["POST", "/api/inbox/mark-all-read"],
      ["POST", "/api/inbox/archive-all"],
      ["POST", "/api/inbox/archive-all-read"],
      ["POST", "/api/inbox/archive-completed"],
      ["POST", `/api/inbox/${ITEM}/read`],
      ["POST", `/api/inbox/${ITEM}/unread`],
      ["POST", `/api/inbox/${ITEM}/archive`],
      ["POST", `/api/inbox/${ITEM}/unarchive`],
    ]);
    expect(calls.slice(4).every((call) => call.body === "{}")).toBe(true);
    expect(calls.slice(0, 4).every((call) => call.body === undefined)).toBe(true);
  });

  it("requires the configured existing audit credential and keeps it out of the body", async () => {
    const calls: Array<{ init?: RequestInit }> = [];
    const fetchImpl = (async (_input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ init });
      return new Response(JSON.stringify({ status: "recorded" }), { status: 201 });
    }) as typeof fetch;
    const client = new MulticaClient({
      serverUrl: "https://api.example.com",
      token: TOKEN,
      fetchImpl,
      auditServiceCredential: "private-audit-credential",
    });
    await client.startMcpInvocationAudit({
      invocation_id: "0b7f4c1e-1111-4222-8333-abcdefabcdef",
      workspace_id: "w1",
      tool_name: "list_inbox",
      target_type: "workspace",
      target_id: "w1",
    });
    const headers = calls[0]?.init?.headers as Record<string, string>;
    const body = String(calls[0]?.init?.body);
    expect(headers["X-Multica-MCP-Service-Credential"]).toBe("private-audit-credential");
    expect(body).not.toContain("private-audit-credential");
    expect(body).not.toContain(TOKEN);
  });

  it("fails before making an audit request when the service credential is absent", async () => {
    let calls = 0;
    const fetchImpl = (async () => {
      calls += 1;
      return new Response("{}", { status: 200 });
    }) as typeof fetch;
    const client = new MulticaClient({ serverUrl: "https://api.example.com", token: TOKEN, fetchImpl });
    await expect(client.startMcpInvocationAudit({
      invocation_id: ITEM,
      workspace_id: "w1",
      tool_name: "list_inbox",
      target_type: "workspace",
      target_id: "w1",
    })).rejects.toThrow("MCP audit service credential is not configured");
    expect(calls).toBe(0);
  });
});
