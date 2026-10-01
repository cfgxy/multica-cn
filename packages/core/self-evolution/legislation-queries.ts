import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { PromptProposalDraftRequest, RetrospectiveConfig } from "../types";

/**
 * Prompt legislation pool keys (RUYI-305 E2). The status filter lives in the
 * key so switching filters refetches rather than reusing the unfiltered page.
 */
export const promptProposalKeys = {
  all: (wsId: string) => ["prompt-proposals", wsId] as const,
  list: (wsId: string, status: string) => [...promptProposalKeys.all(wsId), status] as const,
};

export function promptProposalListOptions(wsId: string, status: string) {
  return queryOptions({
    queryKey: promptProposalKeys.list(wsId, status),
    queryFn: () => api.listPromptProposals(status ? { status } : undefined),
    enabled: !!wsId,
  });
}

/**
 * Every action invalidates the whole pool, not just the active filter: an
 * approval moves a row out of "pending_owner", a rework pulls a gate-failed
 * row back to "draft" — lists under other filters must see the move too.
 */
function useInvalidateProposalPool(wsId: string) {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: promptProposalKeys.all(wsId) });
}

export function useCreatePromptProposal(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (data: PromptProposalDraftRequest) => api.createPromptProposal(data),
    onSuccess: invalidate,
  });
}

export function useUpdatePromptProposalDraft(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (input: { id: string; data: PromptProposalDraftRequest }) =>
      api.updatePromptProposalDraft(input.id, input.data),
    onSuccess: invalidate,
  });
}

export function useSubmitPromptProposal(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (id: string) => api.submitPromptProposal(id),
    onSuccess: invalidate,
  });
}

export function useApprovePromptProposal(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (input: { id: string; confirmDiffPreviewed: boolean }) =>
      api.approvePromptProposal(input.id, input.confirmDiffPreviewed),
    onSuccess: invalidate,
  });
}

export function useBatchApprovePromptProposals(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (input: { ids: string[]; confirmDiffPreviewed: boolean }) =>
      api.batchApprovePromptProposals(input.ids, input.confirmDiffPreviewed),
    onSuccess: invalidate,
  });
}

export function useRejectPromptProposal(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (input: { id: string; reason: string }) => api.rejectPromptProposal(input.id, input.reason),
    onSuccess: invalidate,
  });
}

export function useRestorePromptProposal(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (id: string) => api.restorePromptProposal(id),
    onSuccess: invalidate,
  });
}

export function useReworkPromptProposal(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (id: string) => api.reworkPromptProposal(id),
    onSuccess: invalidate,
  });
}

export function useEnactPromptProposal(wsId: string) {
  const invalidate = useInvalidateProposalPool(wsId);
  return useMutation({
    mutationFn: (id: string) => api.enactPromptProposal(id),
    onSuccess: invalidate,
  });
}

/**
 * Retrospective (RUYI-305 E3) — the built-in scheduler's config and run
 * records. Runs are visible only here: the job never posts issue comments.
 */
export const retrospectiveKeys = {
  all: (wsId: string) => ["retrospective", wsId] as const,
  config: (wsId: string) => [...retrospectiveKeys.all(wsId), "config"] as const,
  runs: (wsId: string) => [...retrospectiveKeys.all(wsId), "runs"] as const,
};

export function retrospectiveConfigOptions(wsId: string) {
  return queryOptions({
    queryKey: retrospectiveKeys.config(wsId),
    queryFn: () => api.getRetrospectiveConfig(),
    enabled: !!wsId,
  });
}

export function retrospectiveRunsOptions(wsId: string) {
  return queryOptions({
    queryKey: retrospectiveKeys.runs(wsId),
    queryFn: () => api.listRetrospectiveRuns(),
    enabled: !!wsId,
  });
}

export function useUpdateRetrospectiveConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (patch: Partial<RetrospectiveConfig>) => api.updateRetrospectiveConfig(patch),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: retrospectiveKeys.config(wsId) });
    },
  });
}

export function useTriggerRetrospectiveRun(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.triggerRetrospectiveRun(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: retrospectiveKeys.runs(wsId) });
      qc.invalidateQueries({ queryKey: promptProposalKeys.all(wsId) });
    },
  });
}
