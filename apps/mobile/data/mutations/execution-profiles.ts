/**
 * Execution profile mutations (RUYI-418 Q10) — invalidation policy mirrors
 * packages/core/execution-profiles/queries.ts one-for-one: profile writes
 * settle-invalidate the whole profiles tree; entry writes only the touched
 * detail + the list (entry_count lives there); activation additionally
 * refreshes the agents tree because it rewrote runtime/model/thinking on
 * real agents.
 */
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type {
  CreateExecutionProfileRequest,
  UpdateExecutionProfileRequest,
  UpsertExecutionProfileEntryRequest,
} from "@multica/core/types";
import { api } from "@/data/api";
import { executionProfileKeys } from "@/data/queries/execution-profiles";
import { agentKeys } from "@/data/queries/agents";
import { useWorkspaceStore } from "@/data/workspace-store";

export function useCreateExecutionProfile() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (body: CreateExecutionProfileRequest) =>
      api.createExecutionProfile(wsId ?? "", body),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: executionProfileKeys.all(wsId ?? "") });
    },
  });
}

export function useUpdateExecutionProfile() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: ({
      profileId,
      patch,
    }: {
      profileId: string;
      patch: UpdateExecutionProfileRequest;
    }) => api.updateExecutionProfile(wsId ?? "", profileId, patch),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: executionProfileKeys.all(wsId ?? "") });
    },
  });
}

export function useDeleteExecutionProfile() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (profileId: string) =>
      api.deleteExecutionProfile(wsId ?? "", profileId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: executionProfileKeys.all(wsId ?? "") });
    },
  });
}

export function useUpsertExecutionProfileEntry() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: ({
      profileId,
      body,
    }: {
      profileId: string;
      body: UpsertExecutionProfileEntryRequest;
    }) => api.upsertExecutionProfileEntry(wsId ?? "", profileId, body),
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({
        queryKey: executionProfileKeys.detail(wsId ?? "", vars.profileId),
      });
      // The list carries entry_count, which this changed.
      qc.invalidateQueries({ queryKey: executionProfileKeys.list(wsId ?? "") });
    },
  });
}

export function useDeleteExecutionProfileEntry() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: ({ profileId, agentId }: { profileId: string; agentId: string }) =>
      api.deleteExecutionProfileEntry(wsId ?? "", profileId, agentId),
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({
        queryKey: executionProfileKeys.detail(wsId ?? "", vars.profileId),
      });
      qc.invalidateQueries({ queryKey: executionProfileKeys.list(wsId ?? "") });
    },
  });
}

export function useActivateExecutionProfile() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (profileId: string) =>
      api.activateExecutionProfile(wsId ?? "", profileId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: executionProfileKeys.all(wsId ?? "") });
      // Activation rewrote runtime/model/thinking on the named agents.
      qc.invalidateQueries({ queryKey: agentKeys.all(wsId) });
    },
  });
}
