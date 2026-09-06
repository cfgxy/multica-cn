import { afterEach, describe, expect, it } from "vitest";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";

import type { Logger } from "../src/log.js";
import { MulticaClient } from "../src/rest.js";
import { createMcpServer, SERVER_NAME } from "../src/server.js";

const TOKEN = `mul_${"a1b2c3d4e5".repeat(4)}`;

function silentLogger(): Logger {
  return { info: () => undefined, error: () => undefined };
}

interface Fixture {
  client: Client;
  cleanup: () => Promise<void>;
}

async function connectServer(
  backendHandler: (pathname: string) => unknown | Promise<unknown>,
  backendStatus = 200,
): Promise<Fixture> {
  const fetchImpl = (async (input: RequestInfo | URL) => {
    const pathname = new URL(String(input)).pathname;
    const body = await backendHandler(pathname);
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
      "create_issue",
      "dispatch_agent",
      "get_issue",
      "list_agents",
      "list_issues",
      "list_projects",
      "list_workspaces",
      "progress_digest",
      "search_issues",
      "update_issue_status",
    ]);
    for (const tool of tools) {
      expect(tool.inputSchema).toMatchObject({ type: "object" });
    }
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
