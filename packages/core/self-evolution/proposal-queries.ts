import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { CreateProposalRequest, VerifyProposalRequest } from "../types";

/**
 * Proposal pool keys (RUYI-265 §A). The status filter lives in the key so
 * switching filters refetches rather than reusing the unfiltered page.
 */
export const proposalKeys = {
  all: (wsId: string) => ["proposals", wsId] as const,
  list: (wsId: string, status: string) => [...proposalKeys.all(wsId), status] as const,
};

export function proposalListOptions(wsId: string, status: string) {
  return queryOptions({
    queryKey: proposalKeys.list(wsId, status),
    queryFn: () => api.listProposals(status ? { status } : undefined),
    enabled: !!wsId,
  });
}

/**
 * Every action invalidates the whole pool, not just the active filter:
 * adopt moves a row out of "draft", reject into "rejected" — lists under
 * other filters must see the move too.
 */
export function useCreateProposal(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateProposalRequest) => api.createProposal(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: proposalKeys.all(wsId) });
    },
  });
}

export function useAdoptProposal(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.adoptProposal(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: proposalKeys.all(wsId) });
    },
  });
}

export function useRejectProposal(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { id: string; reason: string }) => api.rejectProposal(input.id, input.reason),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: proposalKeys.all(wsId) });
    },
  });
}

export function useRestoreProposal(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.restoreProposal(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: proposalKeys.all(wsId) });
    },
  });
}

export function useVerifyProposal(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { id: string; data: VerifyProposalRequest }) => api.verifyProposal(input.id, input.data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: proposalKeys.all(wsId) });
    },
  });
}
