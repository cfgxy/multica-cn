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
import { issueKeys } from "@multica/core/issues/queries";
import { api } from "@/data/api";

export function issueDecisionsOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.decisions(issueId),
    queryFn: () => api.listIssueDecisions(issueId),
    enabled: !!issueId,
  });
}
