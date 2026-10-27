import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/**
 * The key carries `wsId` even though the endpoint does not take one: the scope
 * id alone is not unique across workspaces from the cache's point of view, and
 * a workspace switch must not read the previous workspace's dashboard.
 */
export const promptQualityKeys = {
  all: (wsId: string) => ["prompt-quality", wsId] as const,
  dashboard: (wsId: string, scope: string, scopeId: string, days: number) =>
    [...promptQualityKeys.all(wsId), scope, scopeId, days] as const,
};

export function promptQualityDashboardOptions(
  wsId: string,
  scope: string,
  scopeId: string,
  days: number,
) {
  return queryOptions({
    queryKey: promptQualityKeys.dashboard(wsId, scope, scopeId, days),
    queryFn: () => api.getPromptQualityDashboard(scope, scopeId, { days }),
    // The rollup is a daily job; re-reading it faster than this only costs
    // requests.
    staleTime: 5 * 60 * 1000,
    enabled: wsId !== "" && scope !== "" && scopeId !== "",
  });
}
