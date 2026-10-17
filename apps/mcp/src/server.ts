/**
 * MCP protocol server assembly: registers the tool surface on a
 * (transport-agnostic) MCP SDK Server instance.
 */

import { randomUUID } from "node:crypto";
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
import { MulticaApiError, MulticaRequestError, type MulticaClient } from "./rest.js";
import { ToolInputError } from "./schemas.js";
import { getToolRisk } from "./tool-risk.js";
import { findTool, TOOL_DEFINITIONS } from "./tools.js";

export const SERVER_NAME = "multica-mcp";
export const SERVER_VERSION = "0.1.0";

const AUDITED_INBOX_TOOLS = new Set([
  "list_inbox",
  "list_archived_inbox",
  "get_inbox_unread_count",
  "get_inbox_unread_summary",
  "mark_all_inbox_read",
  "archive_all_inbox",
  "archive_all_read_inbox",
  "archive_completed_inbox",
  "mark_inbox_item_read",
  "mark_inbox_item_unread",
  "archive_inbox_item",
  "unarchive_inbox_item",
]);

const SERVER_INSTRUCTIONS =
  "Tools for working with Multica (AI-native issue tracking) on behalf of the " +
  "authenticated user's personal access token. Start with list_workspaces; every " +
  "tool takes an explicit workspace slug. progress_digest summarizes project " +
  "progress; create_issue captures ideas anywhere; dispatch_agent runs a real " +
  "agent and consumes quota — only on explicit request.";

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
      annotations: getToolRisk(definition.name).annotations,
    })),
  }));

  server.setRequestHandler(CallToolRequestSchema, async (request) => {
    const tool = findTool(request.params.name);
    if (tool === undefined) {
      return errorResult(`Unknown tool: ${request.params.name}`);
    }
    try {
      const args = (request.params.arguments ?? {}) as Record<string, unknown>;
      if (AUDITED_INBOX_TOOLS.has(tool.name)) {
        const workspace = args["workspace"];
        if (typeof workspace !== "string" || workspace.trim().length === 0) {
          throw new Error("'workspace' is required and must be a non-empty string");
        }
        const workspaceId = await client.resolveWorkspaceId(workspace.trim());
        const invocationId = randomUUID();
        const startedAt = Date.now();
        await client.startMcpInvocationAudit({
          invocation_id: invocationId,
          workspace_id: workspaceId,
          tool_name: tool.name,
          target_type: "workspace",
          target_id: workspaceId,
        });
        try {
          const result = await tool.handler(args, client);
          try {
            await client.finishMcpInvocationAudit({
              invocation_id: invocationId,
              status: "success",
              duration_ms: Math.max(0, Date.now() - startedAt),
            });
          } catch {
            const auditFailure = { audit_status: "unrecorded" as const };
            return textResult(JSON.stringify(
              Array.isArray(result) || typeof result !== "object" || result === null
                ? { result, ...auditFailure }
                : { ...result, ...auditFailure },
              null,
              2,
            ));
          }
          return textResult(JSON.stringify(result, null, 2));
        } catch (error) {
          try {
            await client.finishMcpInvocationAudit({
              invocation_id: invocationId,
              status: "failure",
              duration_ms: Math.max(0, Date.now() - startedAt),
              error_code: auditErrorCode(error),
            });
          } catch {
            // Keep the operation error as the caller-facing result. The audit
            // endpoint durably queues an unavailable finish where possible.
          }
          throw error;
        }
      }
      const result = await tool.handler(args, client);
      return textResult(JSON.stringify(result, null, 2));
    } catch (error) {
      return errorResult(redactUnknown(error));
    }
  });

  return server;
}

function auditErrorCode(error: unknown): "invalid_input" | "not_found" | "permission_denied" | "unavailable" | "timeout" | "internal" {
  if (error instanceof ToolInputError) return "invalid_input";
  if (error instanceof MulticaApiError) {
    if (error.status === 400 || error.status === 422) return "invalid_input";
    if (error.status === 401 || error.status === 403) return "permission_denied";
    if (error.status === 404) return "not_found";
    if (error.status === 408 || error.status === 504) return "timeout";
    if (error.status >= 500) return "unavailable";
  }
  if (error instanceof MulticaRequestError) return "unavailable";
  return "internal";
}

function textResult(text: string): CallToolResult {
  const content: TextContent = { type: "text", text };
  return { content: [content] };
}

function errorResult(error: unknown): CallToolResult {
  const content: TextContent = { type: "text", text: redactUnknown(error) };
  return { content: [content], isError: true };
}
