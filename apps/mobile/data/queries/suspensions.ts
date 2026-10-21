/**
 * Issue suspension queries (RUYI-690).
 *
 * Same arrangement as `data/queries/decisions.ts`: query keys come from
 * core's `issueKeys` (pure data — on the mobile import whitelist) so
 * web/desktop and mobile address the same cache entries, while the queryFn
 * goes through the mobile-owned fetch wrapper (`@/data/api`).
 */
import { queryOptions } from "@tanstack/react-query";
import { issueKeys } from "@multica/core/issues/queries";
import { api } from "@/data/api";

export function issueSuspensionsOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.suspensions(issueId),
    queryFn: () => api.listIssueSuspensions(issueId),
    enabled: !!issueId,
  });
}

/**
 * Workspace suspension aggregation (挂起中心). The status window rides in
 * the key — active is the default view, `all` is the audit window — matching
 * core's `workspaceSuspensionsQueryOptions` key layout.
 */
export function workspaceSuspensionsOptions(wsId: string | null, status?: string) {
  return queryOptions({
    queryKey: [...issueKeys.workspaceSuspensions(wsId), status ?? "active"],
    queryFn: async () => {
      if (!wsId) return { suspensions: [], total: 0 };
      return api.listWorkspaceSuspensions(wsId, status ? { status } : undefined);
    },
    enabled: !!wsId,
  });
}
