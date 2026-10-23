/**
 * Decision card queries (RUYI-345).
 *
 * Query key shape is shared with web/desktop via core's `issueKeys`
 * (pure data — on the mobile import whitelist), so core's
 * `patchDecisionInCache` — used by the WS `decision:updated` handler —
 * patches the same cache entry this query fills. The queryFn itself
 * goes through the mobile-owned fetch wrapper (`@/data/api`), never
 * core's unconfigured api singleton.
 */
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import {
  decisionInboxKeys,
  type WorkspaceDecisionInboxFilters,
} from "@multica/core/issues/decisions";
import {
  decisionRequestKeys,
  type DecisionRequestsListFilters,
} from "@multica/core/issues/decision-requests";
import type { DecisionRequestDetail, WorkspaceDecisionInbox } from "@multica/core/types";
import { issueKeys } from "@multica/core/issues/queries";
import { api } from "@/data/api";

const EMPTY_INBOX: WorkspaceDecisionInbox = {
  items: [],
  counts: { open: 0, answered: 0, cancelled: 0 },
  next_cursor: null,
  next_cursor_id: null,
};

export function issueDecisionsOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.decisions(issueId),
    queryFn: () => api.listIssueDecisions(issueId),
    enabled: !!issueId,
  });
}

// Workspace-level aggregation (RUYI-494). The key is core's
// `decisionInboxKeys` (pure data — same import whitelist as `issueKeys`
// above) so the workspace-layout realtime hook and web/desktop invalidations
// address the same entry; the queryFn stays on the mobile-owned api wrapper.
export function workspaceDecisionInboxOptions(
  wsId: string | null,
  filters?: WorkspaceDecisionInboxFilters,
) {
  return queryOptions({
    queryKey: filters
      ? decisionInboxKeys.inbox(wsId, filters)
      : decisionInboxKeys.workspace(wsId),
    // The signal rides to fetch (mobile CLAUDE.md §Lesson 4): a stale
    // window's request must not outlive the user.
    queryFn: async ({ signal }) => {
      if (!wsId) return EMPTY_INBOX;
      return api.listWorkspaceDecisionInbox(wsId, filters, { signal });
    },
    enabled: !!wsId,
  });
}

// RUYI-864 keyset pagination of the decision list — mirrors
// core's `workspaceDecisionInboxPageOptions` (two-field cursor, the pair
// travels together, a short page ends the walk). The queryFn stays on the
// mobile-owned api wrapper; the empty state carries the cursor fields so a
// no-workspace page still satisfies the shared shape.
export function workspaceDecisionInboxPageOptions(
  wsId: string | null,
  filters: WorkspaceDecisionInboxFilters = {},
) {
  return infiniteQueryOptions({
    queryKey: decisionInboxKeys.inbox(wsId, filters),
    initialPageParam: null as { after_created_at: string; after_id: string } | null,
    queryFn: async ({ pageParam, signal }) => {
      if (!wsId) return EMPTY_INBOX;
      return api.listWorkspaceDecisionInbox(
        wsId,
        {
          ...filters,
          ...(pageParam ?? {}),
        },
        { signal },
      );
    },
    getNextPageParam: (lastPage) =>
      lastPage.next_cursor && lastPage.next_cursor_id
        ? {
            after_created_at: lastPage.next_cursor,
            after_id: lastPage.next_cursor_id,
          }
        : undefined,
    enabled: !!wsId,
  });
}

// Agent authorization requests (RUYI-630): the mobile decisions tab's
// authorization section. Key is core's `decisionRequestKeys` (pure data)
// so web/desktop invalidations and the workspace realtime hook address the
// same entry; the queryFn stays on the mobile-owned api wrapper.
// RUYI-864: the window takes one real lifecycle status plus the scan
// direction — the pending queue (order=asc, limit=1) and the history
// surface (order=desc) are two server-ordered calls, no client state.
export function workspaceDecisionRequestsOptions(
  wsId: string | null,
  filters?: DecisionRequestsListFilters,
) {
  return queryOptions({
    queryKey: filters
      ? [...decisionRequestKeys.workspace(wsId), filters]
      : decisionRequestKeys.workspace(wsId),
    queryFn: async ({ signal }) => {
      if (!wsId)
        return {
          items: [],
          counts: { pending: 0, approved: 0, closed: 0, executed: 0, history: 0 },
          next_cursor: null,
          next_cursor_id: null,
        };
      return api.listDecisionRequests(wsId, filters, { signal });
    },
    enabled: !!wsId,
  });
}

// RUYI-864 rework: keyset pagination of the authorization history window —
// mirrors core's `workspaceDecisionInboxPageOptions` shape: two-field
// cursor, the pair travels together, a short page ends the walk. The
// `status: "history"` filter is THE window (every non-pending row), the
// same set the counts' `history` total badges, so an expanded entry can
// never show "total > 0, rows empty".
export function workspaceDecisionRequestsHistoryPageOptions(wsId: string | null) {
  return infiniteQueryOptions({
    queryKey: [
      ...decisionRequestKeys.workspace(wsId),
      { status: "history", order: "desc" },
      "pages",
    ],
    initialPageParam: null as { after_created_at: string; after_id: string } | null,
    queryFn: async ({ pageParam, signal }) => {
      if (!wsId)
        return {
          items: [],
          counts: { pending: 0, approved: 0, closed: 0, executed: 0, history: 0 },
          next_cursor: null,
          next_cursor_id: null,
        };
      return api.listDecisionRequests(
        wsId,
        {
          status: "history",
          order: "desc",
          limit: 20,
          ...(pageParam ?? {}),
        },
        { signal },
      );
    },
    getNextPageParam: (lastPage) =>
      lastPage.next_cursor && lastPage.next_cursor_id
        ? {
            after_created_at: lastPage.next_cursor,
            after_id: lastPage.next_cursor_id,
          }
        : undefined,
    enabled: !!wsId,
  });
}

export function decisionRequestDetailOptions(wsId: string | null, requestId: string | null) {
  return queryOptions({
    queryKey: [...decisionRequestKeys.workspace(wsId), requestId ?? "none", "detail"] as const,
    queryFn: async (): Promise<DecisionRequestDetail> => {
      if (!wsId || !requestId) throw new Error("decision request detail requires ids");
      return api.getDecisionRequest(wsId, requestId);
    },
    enabled: !!wsId && !!requestId,
  });
}
