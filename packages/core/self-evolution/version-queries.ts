import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { SnapshotPromptGovernanceVersionRequest } from "../types/prompt-version";
import { workspaceKeys } from "../workspace/queries";

/**
 * Prompt governance version lifecycle queries (RUYI-285).
 *
 * The version line is the write side of what the quality dashboard measures.
 * A switch appends a row AND rewrites the tier's business column, so both
 * families of cache must move together — the agent detail and list caches
 * carry the effective instructions, and leaving them stale would show content
 * the tier stopped running after the very action the user just confirmed.
 * A snapshot (RUYI-285 rework) appends a row and touches nothing else, so it
 * invalidates the version list only.
 *
 * Nothing here is optimistic. The server can refuse a write (secret scan 422,
 * empty-content 400, blank-over-live 409) after the user has acted, so a row
 * patched into cache before the answer could be a row the server never
 * accepted.
 */
export const promptVersionKeys = {
  all: (wsId: string) => ["prompt-governance-versions", wsId] as const,
  list: (wsId: string, scope: string, scopeId: string) =>
    [...promptVersionKeys.all(wsId), scope, scopeId] as const,
};

export function promptGovernanceVersionsOptions(
  wsId: string,
  scope: string,
  scopeId: string,
) {
  return queryOptions({
    queryKey: promptVersionKeys.list(wsId, scope, scopeId),
    queryFn: () => api.listPromptGovernanceVersions(scope, scopeId),
    enabled: wsId !== "" && scope !== "" && scopeId !== "",
  });
}

function useInvalidateAfterVersionWrite(wsId: string, scope: string, scopeId: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: promptVersionKeys.all(wsId) });
    // The business column moved with the version row; agent caches hold it.
    if (scope === "agent" && scopeId !== "") {
      qc.invalidateQueries({ queryKey: workspaceKeys.agent(wsId, scopeId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    }
  };
}

export function useSnapshotPromptVersion(wsId: string, scope: string, scopeId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: SnapshotPromptGovernanceVersionRequest = {}) =>
      api.snapshotPromptGovernanceVersion(scope, scopeId, data),
    // The snapshot writes the version row only — the business column it read
    // from is untouched, so no entity cache needs to move with it.
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: promptVersionKeys.all(wsId) });
    },
  });
}

export function useSwitchPromptVersion(wsId: string, scope: string, scopeId: string) {
  const invalidate = useInvalidateAfterVersionWrite(wsId, scope, scopeId);
  return useMutation({
    mutationFn: (version: number) => api.switchPromptGovernanceVersion(scope, scopeId, version),
    onSuccess: invalidate,
  });
}
