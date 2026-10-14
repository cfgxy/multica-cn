import {
  queryOptions,
  type MutationFunction,
} from "@tanstack/react-query";
import { api } from "../api";
import type {
  DecisionRequest,
  DecisionRequestDetail,
  DecisionRequestsList,
} from "../types";
import { issueKeys } from "./queries";

// Agent authorization requests (RUYI-630): the decision-center's summary
// surface for the Issue-independent authorization carrier. Server state
// owned by React Query like every other resource; the list is small
// (requests are rare) so a plain per-workspace query with invalidation on
// lifecycle events is the whole strategy — same shape as the decision inbox.

export const decisionRequestKeys = {
  all: () => [...issueKeys.decisionsAll(), "decision-requests"] as const,
  workspace: (workspaceId: string | null | undefined) =>
    [...decisionRequestKeys.all(), workspaceId ?? "none"] as const,
};

export function workspaceDecisionRequestsQueryOptions(workspaceId: string | null | undefined) {
  return queryOptions({
    queryKey: decisionRequestKeys.workspace(workspaceId),
    queryFn: async (): Promise<DecisionRequestsList> => {
      if (!workspaceId) {
        return { items: [], counts: { pending: 0, closed: 0, executed: 0 } };
      }
      return api.listDecisionRequests(workspaceId);
    },
    enabled: !!workspaceId,
  });
}

export function decisionRequestDetailQueryOptions(
  workspaceId: string | null | undefined,
  requestId: string | null | undefined,
) {
  return queryOptions({
    queryKey: [...decisionRequestKeys.workspace(workspaceId), requestId ?? "none", "detail"] as const,
    queryFn: async (): Promise<DecisionRequestDetail> => {
      if (!workspaceId || !requestId) throw new Error("decision request detail requires ids");
      return api.getDecisionRequest(workspaceId, requestId);
    },
    enabled: !!workspaceId && !!requestId,
  });
}

export const answerDecisionRequest: MutationFunction<
  DecisionRequest,
  { workspaceId: string; requestId: string; decision: "approve" | "deny" }
> = ({ workspaceId, requestId, decision }) =>
  api.answerDecisionRequest(workspaceId, requestId, decision);

export const cancelDecisionRequest: MutationFunction<
  { status: string },
  { workspaceId: string; requestId: string }
> = ({ workspaceId, requestId }) => api.cancelDecisionRequest(workspaceId, requestId);
