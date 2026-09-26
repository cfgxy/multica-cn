import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  CreatePromptQuizItemRequest,
  UpdatePromptQuizItemRequest,
} from "../types/prompt-quiz";

/**
 * Quiz bank and regression reading queries (RUYI-185).
 *
 * The bank is workspace-scoped and the reading is prompt-scope-scoped, so they
 * are two key families rather than one. Both carry `wsId` for the reason
 * `promptQualityKeys` does: a scope id alone is not unique across workspaces
 * from the cache's point of view.
 *
 * Nothing here is optimistic. A bank write changes what future runs are
 * measured against, the author stays on the page to see the result, and the
 * server can refuse a body the isolation gate rejects — so a row patched into
 * cache before the server answered could be a row the server never accepted.
 */
export const promptQuizKeys = {
  all: (wsId: string) => ["prompt-quiz", wsId] as const,
  items: (wsId: string, activeOnly: boolean) =>
    [...promptQuizKeys.all(wsId), "items", activeOnly] as const,
  baseline: (wsId: string, scope: string, scopeId: string) =>
    [...promptQuizKeys.all(wsId), "baseline", scope, scopeId] as const,
};

export function promptQuizItemsOptions(wsId: string, activeOnly = false) {
  return queryOptions({
    queryKey: promptQuizKeys.items(wsId, activeOnly),
    queryFn: () => api.listPromptQuizItems({ activeOnly }),
    enabled: wsId !== "",
  });
}

export function promptQuizBaselineOptions(wsId: string, scope: string, scopeId: string) {
  return queryOptions({
    queryKey: promptQuizKeys.baseline(wsId, scope, scopeId),
    queryFn: () => api.getPromptQuizBaseline(scope, scopeId),
    // The sweep runs hourly; re-reading faster than that only costs requests.
    staleTime: 5 * 60 * 1000,
    enabled: wsId !== "" && scope !== "" && scopeId !== "",
  });
}

export function useCreatePromptQuizItem(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreatePromptQuizItemRequest) => api.createPromptQuizItem(body),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: promptQuizKeys.all(wsId) });
    },
  });
}

export function useUpdatePromptQuizItem(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ itemId, patch }: { itemId: string; patch: UpdatePromptQuizItemRequest }) =>
      api.updatePromptQuizItem(itemId, patch),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: promptQuizKeys.all(wsId) });
    },
  });
}

export function useDeletePromptQuizItem(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (itemId: string) => api.deletePromptQuizItem(itemId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: promptQuizKeys.all(wsId) });
    },
  });
}
