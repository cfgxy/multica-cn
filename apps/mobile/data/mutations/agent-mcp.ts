/**
 * Agent MCP assignment mutations (RUYI-418 B3). Every write returns the
 * resulting assignment list, so the cache never guesses — server response is
 * written straight into the assigned key. The agent detail is settle-
 * invalidated because its mcp_config JSON mirrors assigned servers.
 */
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/data/api";
import { agentMcpKeys } from "@/data/queries/agent-mcp";
import { agentKeys } from "@/data/queries/agents";
import { useWorkspaceStore } from "@/data/workspace-store";

export function useAddAgentMcpServer(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["addAgentMcpServer", agentId] as const,
    mutationFn: (serverId: string) => api.addAgentMcpServer(agentId, serverId),
    onSuccess: (servers) => {
      qc.setQueryData(agentMcpKeys.assigned(wsId, agentId), servers);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.detail(wsId, agentId) });
    },
  });
}

export function useSetAgentMcpServerEnabled(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["setAgentMcpServerEnabled", agentId] as const,
    mutationFn: ({ serverId, enabled }: { serverId: string; enabled: boolean }) =>
      api.setAgentMcpServerEnabled(agentId, serverId, enabled),
    onSuccess: (servers) => {
      qc.setQueryData(agentMcpKeys.assigned(wsId, agentId), servers);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.detail(wsId, agentId) });
    },
  });
}

export function useRemoveAgentMcpServer(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["removeAgentMcpServer", agentId] as const,
    mutationFn: (serverId: string) => api.removeAgentMcpServer(agentId, serverId),
    onSuccess: (servers) => {
      qc.setQueryData(agentMcpKeys.assigned(wsId, agentId), servers);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.detail(wsId, agentId) });
    },
  });
}
