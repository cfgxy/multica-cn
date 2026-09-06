/**
 * MCP protocol server assembly: registers the RUYI-82 tool surface on a
 * (transport-agnostic) MCP SDK Server instance.
 */

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import type {
  CallToolResult,
  TextContent,
  Tool,
} from "@modelcontextprotocol/sdk/types.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

import { redactUnknown } from "./redact.js";
import type { MulticaClient } from "./rest.js";
import { findTool, TOOL_DEFINITIONS } from "./tools.js";

export const SERVER_NAME = "multica-mcp";
export const SERVER_VERSION = "0.1.0";

const SERVER_INSTRUCTIONS =
  "Tools for working with Multica (AI-native issue tracking) on behalf of the " +
  "authenticated user's personal access token. Start with list_workspaces; every " +
  "tool takes an explicit workspace slug. progress_digest summarizes project " +
  "progress; create_issue captures ideas anywhere; dispatch_agent runs a real " +
  "agent and consumes quota — only on explicit request.";

const READ_ONLY_TOOLS: ReadonlySet<string> = new Set([
  "list_workspaces",
  "list_agents",
  "list_issues",
  "get_issue",
  "search_issues",
  "progress_digest",
]);

export function createMcpServer(client: MulticaClient): Server {
  const server = new Server(
    { name: SERVER_NAME, version: SERVER_VERSION },
    {
      instructions: SERVER_INSTRUCTIONS,
      capabilities: { tools: { listChanged: false } },
    },
  );

  server.setRequestHandler(ListToolsRequestSchema, async () => ({
    tools: TOOL_DEFINITIONS.map((definition): Tool => ({
      name: definition.name,
      description: definition.description,
      inputSchema: definition.inputSchema,
      annotations: {
        readOnlyHint: READ_ONLY_TOOLS.has(definition.name),
        openWorldHint: true,
      },
    })),
  }));

  server.setRequestHandler(CallToolRequestSchema, async (request) => {
    const tool = findTool(request.params.name);
    if (tool === undefined) {
      return errorResult(`Unknown tool: ${request.params.name}`);
    }
    try {
      const args = (request.params.arguments ?? {}) as Record<string, unknown>;
      const result = await tool.handler(args, client);
      return textResult(JSON.stringify(result, null, 2));
    } catch (error) {
      return errorResult(redactUnknown(error));
    }
  });

  return server;
}

function textResult(text: string): CallToolResult {
  const content: TextContent = { type: "text", text };
  return { content: [content] };
}

function errorResult(error: unknown): CallToolResult {
  const content: TextContent = { type: "text", text: redactUnknown(error) };
  return { content: [content], isError: true };
}
