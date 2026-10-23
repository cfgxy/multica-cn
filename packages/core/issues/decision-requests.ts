import {
  infiniteQueryOptions,
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

/**
 * RUYI-864 list window for authorization requests. `status` is one of the
 * row's real lifecycle statuses or the `history` terminal-state window
 * (every non-pending row — the same set the counts' `history` total
 * reports), and `order` picks the scan direction: 'asc' is the oldest-first
 * pending queue, 'desc' the newest-first history surface (the server
 * default when absent). `after_created_at`/`after_id` are the keyset cursor
 * pair: both together or neither — the comparison follows the active
 * direction, so the same pair pages a desc and an asc walk.
 */
export interface DecisionRequestsListFilters {
  status?: string;
  limit?: number;
  order?: "asc" | "desc";
  after_created_at?: string;
  after_id?: string;
}

export function workspaceDecisionRequestsQueryOptions(
  workspaceId: string | null | undefined,
  filters?: DecisionRequestsListFilters,
) {
  return queryOptions({
    queryKey: filters
      ? [...decisionRequestKeys.workspace(workspaceId), filters]
      : decisionRequestKeys.workspace(workspaceId),
    queryFn: async (): Promise<DecisionRequestsList> => {
      if (!workspaceId) {
        return {
          items: [],
          counts: { pending: 0, approved: 0, closed: 0, executed: 0, history: 0 },
          next_cursor: null,
          next_cursor_id: null,
        };
      }
      return api.listDecisionRequests(workspaceId, filters);
    },
    enabled: !!workspaceId,
  });
}

/**
 * RUYI-864 rework: keyset pagination of the authorization HISTORY window —
 * `status="history"` is the server's terminal-state window (every non-pending
 * row), newest-first. It is the SAME set the counts' `history` total badges,
 * so an expanded entry can never show "total > 0, rows empty": the newest
 * 50 rows all being pending no longer leaves the entry pointing at an empty
 * expansion. The cursor pair travels together (the server rejects a lone
 * half with 400); a short page ends the walk.
 */
export function workspaceDecisionRequestsHistoryPageOptions(
  workspaceId: string | null | undefined,
) {
  return infiniteQueryOptions({
    queryKey: [
      ...decisionRequestKeys.workspace(workspaceId),
      { status: "history", order: "desc" },
      "pages",
    ],
    initialPageParam: null as { after_created_at: string; after_id: string } | null,
    queryFn: async ({ pageParam }): Promise<DecisionRequestsList> => {
      if (!workspaceId) {
        return {
          items: [],
          counts: { pending: 0, approved: 0, closed: 0, executed: 0, history: 0 },
          next_cursor: null,
          next_cursor_id: null,
        };
      }
      return api.listDecisionRequests(workspaceId, {
        status: "history",
        order: "desc",
        limit: 20,
        ...(pageParam ?? {}),
      });
    },
    getNextPageParam: (lastPage) =>
      lastPage.next_cursor && lastPage.next_cursor_id
        ? {
            after_created_at: lastPage.next_cursor,
            after_id: lastPage.next_cursor_id,
          }
        : undefined,
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
