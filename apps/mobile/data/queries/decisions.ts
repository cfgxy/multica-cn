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
import { queryOptions } from "@tanstack/react-query";
import { decisionInboxKeys } from "@multica/core/issues/decisions";
import { decisionRequestKeys } from "@multica/core/issues/decision-requests";
import { issueKeys } from "@multica/core/issues/queries";
import { api } from "@/data/api";

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
export function workspaceDecisionInboxOptions(wsId: string | null) {
  return queryOptions({
    queryKey: decisionInboxKeys.workspace(wsId),
    queryFn: async () => {
      if (!wsId) return { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
      return api.listWorkspaceDecisionInbox(wsId);
    },
    enabled: !!wsId,
  });
}

// Agent authorization requests (RUYI-630): the mobile decisions tab's
// authorization section. Key is core's `decisionRequestKeys` (pure data)
// so web/desktop invalidations and the workspace realtime hook address the
// same entry; the queryFn stays on the mobile-owned api wrapper.
export function workspaceDecisionRequestsOptions(wsId: string | null) {
  return queryOptions({
    queryKey: decisionRequestKeys.workspace(wsId),
    queryFn: async () => {
      if (!wsId) return { items: [], counts: { pending: 0, closed: 0, executed: 0 } };
      return api.listDecisionRequests(wsId);
    },
    enabled: !!wsId,
  });
}
