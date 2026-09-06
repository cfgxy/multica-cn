import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { ApiError } from "../api";
import type {
  CreateExecutionProfileRequest,
  UpdateExecutionProfileRequest,
  UpsertExecutionProfileEntryRequest,
} from "../types/execution-profile";
import { workspaceKeys } from "../workspace/queries";

// Execution profiles (RUYI-57): named per-agent execution configuration for a
// workspace. Keys carry the workspace id so switching workspaces cannot serve
// another workspace's profiles from cache.
//
// Every mutation here also invalidates the workspace agent list: activation
// rewrites the agents' runtime and model, so a members page still showing the
// pre-activation rows would contradict the profile the picker now marks
// active.
export const executionProfileKeys = {
  all: (wsId: string) => ["execution-profiles", wsId] as const,
  list: (wsId: string) => [...executionProfileKeys.all(wsId), "list"] as const,
  detail: (wsId: string, profileId: string) =>
    [...executionProfileKeys.all(wsId), "detail", profileId] as const,
};

export function executionProfileListOptions(wsId: string) {
  return queryOptions({
    queryKey: executionProfileKeys.list(wsId),
    queryFn: () => api.listExecutionProfiles(wsId),
  });
}

export function executionProfileDetailOptions(wsId: string, profileId: string) {
  return queryOptions({
    queryKey: executionProfileKeys.detail(wsId, profileId),
    queryFn: () => api.getExecutionProfile(wsId, profileId),
    enabled: profileId !== "",
  });
}

export function useCreateExecutionProfile(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateExecutionProfileRequest) =>
      api.createExecutionProfile(wsId, body),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: executionProfileKeys.all(wsId) });
    },
  });
}

export function useUpdateExecutionProfile(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      profileId,
      patch,
    }: {
      profileId: string;
      patch: UpdateExecutionProfileRequest;
    }) => api.updateExecutionProfile(wsId, profileId, patch),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: executionProfileKeys.all(wsId) });
    },
  });
}

export function useDeleteExecutionProfile(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (profileId: string) =>
      api.deleteExecutionProfile(wsId, profileId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: executionProfileKeys.all(wsId) });
    },
  });
}

export function useUpsertExecutionProfileEntry(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      profileId,
      body,
    }: {
      profileId: string;
      body: UpsertExecutionProfileEntryRequest;
    }) => api.upsertExecutionProfileEntry(wsId, profileId, body),
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({
        queryKey: executionProfileKeys.detail(wsId, vars.profileId),
      });
      // The list carries entry_count, which this changed.
      qc.invalidateQueries({ queryKey: executionProfileKeys.list(wsId) });
    },
  });
}

export function useDeleteExecutionProfileEntry(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      profileId,
      agentId,
    }: {
      profileId: string;
      agentId: string;
    }) => api.deleteExecutionProfileEntry(wsId, profileId, agentId),
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({
        queryKey: executionProfileKeys.detail(wsId, vars.profileId),
      });
      qc.invalidateQueries({ queryKey: executionProfileKeys.list(wsId) });
    },
  });
}

// Activation rewrites agents, so it invalidates the agent list as well as the
// profile tree. Invalidated on settle rather than on success: a partial
// activation still moved some agents, and so does a request that failed after
// the server committed.
export function useActivateExecutionProfile(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (profileId: string) =>
      api.activateExecutionProfile(wsId, profileId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: executionProfileKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    },
  });
}

// A duplicate profile name comes back as a 409 the rename field shows inline.
// Anything else collapses to null so callers fall through to the generic
// error path.
export function parseExecutionProfileNameConflict(err: unknown): string | null {
  if (!(err instanceof ApiError)) return null;
  if (err.status !== 409) return null;
  const body = err.body;
  if (body && typeof body === "object" && "error" in body) {
    const message = (body as { error?: unknown }).error;
    if (typeof message === "string" && message !== "") return message;
  }
  return err.message;
}
