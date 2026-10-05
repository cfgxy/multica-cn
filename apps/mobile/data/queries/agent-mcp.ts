/**
 * Agent MCP server queries (RUYI-418 B3) — the workspace library servers
 * assigned to one agent, plus the workspace library itself for the add sheet.
 * The agent's own mcp_config JSON and the runtime read-only inventory are
 * NOT queries: the former lives on the agent payload, the latter rides the
 * runtime-capabilities snapshot.
 */
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const agentMcpKeys = {
  all: (wsId: string | null, agentId: string) =>
    ["agent-mcp", wsId, agentId] as const,
  assigned: (wsId: string | null, agentId: string) =>
    [...agentMcpKeys.all(wsId, agentId), "assigned"] as const,
};

export const workspaceMcpKeys = {
  all: (wsId: string | null) => ["workspace-mcp-servers", wsId] as const,
  list: (wsId: string | null) => [...workspaceMcpKeys.all(wsId), "list"] as const,
};

export function agentMcpOptions(wsId: string | null, agentId: string) {
  return queryOptions({
    queryKey: agentMcpKeys.assigned(wsId, agentId),
    queryFn: ({ signal }) => api.listAgentMcpServers(agentId, { signal }),
    enabled: Boolean(wsId && agentId),
  });
}

export function workspaceMcpServersOptions(wsId: string | null) {
  return queryOptions({
    queryKey: workspaceMcpKeys.list(wsId),
    queryFn: ({ signal }) => api.listWorkspaceMcpServers(wsId ?? "", { signal }),
    enabled: Boolean(wsId),
  });
}
