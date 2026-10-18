import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/data/api";
import { runtimeKeys } from "@/data/queries/runtimes";
import { agentKeys } from "@/data/queries/agents";
import { agentTaskSnapshotKeys } from "@/data/queries/agent-task-snapshot";
import { useWorkspaceStore } from "@/data/workspace-store";

export function useUpdateRuntime() {
  const queryClient = useQueryClient();
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);

  return useMutation({
    mutationFn: ({ runtimeId, customName }: { runtimeId: string; customName: string }) =>
      api.updateRuntime(runtimeId, { custom_name: customName }),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

export function useDeleteRuntime() {
  const queryClient = useQueryClient();
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    void queryClient.invalidateQueries({ queryKey: agentKeys.all(wsId) });
    void queryClient.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
  };

  return useMutation({
    mutationFn: (runtimeId: string) => api.deleteRuntime(runtimeId),
    onSettled: invalidate,
  });
}

export function useUnbindAgentsAndDeleteRuntime() {
  const queryClient = useQueryClient();
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);

  return useMutation({
    mutationFn: ({
      runtimeId,
      expectedActiveAgentIds,
    }: {
      runtimeId: string;
      expectedActiveAgentIds: string[];
    }) => api.unbindAgentsAndDeleteRuntime(runtimeId, expectedActiveAgentIds),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      void queryClient.invalidateQueries({ queryKey: agentKeys.all(wsId) });
      void queryClient.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
    },
  });
}
