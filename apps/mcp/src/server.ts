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
import { MCPFileAccessError, MCPFileAccessResult, MulticaApiError, MulticaRequestError, type MulticaClient } from "./rest.js";
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
const AUDITED_FILE_TOOLS = new Set(["list_files", "get_file_metadata", "read_file_chunk"]);

const SERVER_INSTRUCTIONS =
  "Tools for working with Multica (AI-native issue tracking) on behalf of the " +
  "authenticated user's personal access token. Start with list_workspaces; every " +
  "tool takes an explicit workspace slug. progress_digest summarizes project " +
  "progress; create_issue captures ideas anywhere; dispatch_agent runs a real " +
  "agent and consumes quota — only on explicit request.";

export function createMcpServer(
  client: MulticaClient,
  reportedClient?: { name: string; version: string },
): Server {
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
      await recordPreAuth(client, randomUUID(), "unknown_tool");
      return errorResult(`Unknown tool: ${request.params.name}`);
    }
    try {
      const args = (request.params.arguments ?? {}) as Record<string, unknown>;
      if (AUDITED_FILE_TOOLS.has(tool.name) && (typeof args["workspace"] !== "string" || args["workspace"].trim().length === 0)) {
        await recordPreAuth(client, randomUUID(), "missing_workspace", tool.name as FileToolName);
        throw new ToolInputError("'workspace' is required and must be a non-empty string");
      }
      if (AUDITED_INBOX_TOOLS.has(tool.name) || AUDITED_FILE_TOOLS.has(tool.name)) {
        const workspace = args["workspace"];
        if (typeof workspace !== "string" || workspace.trim().length === 0) {
          throw new Error("'workspace' is required and must be a non-empty string");
        }
        let workspaceId: string;
        try {
          workspaceId = await client.resolveWorkspaceId(workspace.trim());
        } catch (error) {
          if (AUDITED_FILE_TOOLS.has(tool.name)) {
            const reason = error instanceof MulticaApiError && error.status === 401 ? "unauthenticated_actor" : "invalid_workspace";
            await recordPreAuth(client, randomUUID(), reason, tool.name as FileToolName);
          }
          throw error;
        }
        const invocationId = randomUUID();
        const startedAt = Date.now();
        const isFileTool = AUDITED_FILE_TOOLS.has(tool.name);
        const clientVersion = server.getClientVersion() ?? reportedClient;
        try {
          await client.startMcpInvocationAudit({
            invocation_id: invocationId,
            workspace_id: workspaceId,
            tool_name: tool.name,
            ...(clientVersion?.name ? { reported_client_name: clientVersion.name } : {}),
            ...(clientVersion?.version ? { reported_client_version: clientVersion.version } : {}),
            ...(isFileTool
              ? { instance_id: client.mcpInstanceIdentity }
              : { target_type: "workspace" as const, target_id: workspaceId }),
          });
        } catch (error) {
          if (isFileTool) await recordPreAuth(client, invocationId, "entry_rejected", tool.name as FileToolName);
          throw error;
        }
        try {
          const result = await tool.handler(
            isFileTool ? { ...args, _mcp_invocation_id: invocationId } : args,
            client,
          );
          const operationId = result instanceof MCPFileAccessResult ? result.operationId : undefined;
          const visibleResult = result instanceof MCPFileAccessResult ? result.result : result;
          try {
            const auditStatus = await client.finishMcpInvocationAudit({
              invocation_id: invocationId,
              status: "success",
              duration_ms: Math.max(0, Date.now() - startedAt),
              ...(operationId !== undefined ? { operation_id: operationId } : {}),
              ...(isFileTool && client.mcpInstanceIdentity ? { instance_id: client.mcpInstanceIdentity } : {}),
            });
            if (auditStatus !== "recorded") {
              return textResult(JSON.stringify({ result: visibleResult, audit_status: auditStatus }, null, 2));
            }
          } catch {
            const auditFailure = { audit_status: "unrecorded" as const };
            return textResult(JSON.stringify(
              Array.isArray(visibleResult) || typeof visibleResult !== "object" || visibleResult === null
                ? { result: visibleResult, ...auditFailure }
                : { ...visibleResult, ...auditFailure },
              null,
              2,
            ));
          }
          return textResult(JSON.stringify(visibleResult, null, 2));
        } catch (error) {
          let auditStatus: "recorded" | "queued" | "unrecorded" = "unrecorded";
          try {
            auditStatus = await client.finishMcpInvocationAudit({
              invocation_id: invocationId,
              status: isFileTool && (error instanceof ToolInputError || error instanceof MulticaApiError && error.status < 500 || error instanceof MCPFileAccessError && error.status === "rejected")
                ? "refused"
                : "failure",
              duration_ms: Math.max(0, Date.now() - startedAt),
              error_code: auditErrorCode(error),
              ...(error instanceof MCPFileAccessError ? { operation_id: error.operationId } : {}),
              ...(isFileTool && client.mcpInstanceIdentity ? { instance_id: client.mcpInstanceIdentity } : {}),
            });
          } catch {
            // The original error remains primary; the response below reports the audit failure.
          }
          const visibleError = redactUnknown(error);
          throw new MCPInvocationError(visibleError, auditStatus);
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

type FileToolName = "list_files" | "get_file_metadata" | "read_file_chunk";

class MCPInvocationError extends Error {
  constructor(message: string, auditStatus: "recorded" | "queued" | "unrecorded") {
    super(`${message} (audit_status: ${auditStatus})`);
    this.name = "MCPInvocationError";
  }
}

async function recordPreAuth(
  client: MulticaClient,
  invocationId: string,
  reason: "unknown_tool" | "missing_workspace" | "invalid_workspace" | "unauthenticated_actor" | "entry_rejected",
  fileToolName?: FileToolName,
): Promise<void> {
  try {
    await client.recordMcpPreAuthRejection({
      invocation_id: invocationId,
      reason_code: reason,
      ...(fileToolName !== undefined ? { file_tool_name: fileToolName } : {}),
    });
  } catch {
    // Rejection auditing fails closed for execution; there is no safe retry path here.
  }
}

function auditErrorCode(error: unknown): "invalid_input" | "not_found" | "permission_denied" | "unavailable" | "timeout" | "internal" {
  if (error instanceof MCPFileAccessError) {
    return error.code;
  }
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
