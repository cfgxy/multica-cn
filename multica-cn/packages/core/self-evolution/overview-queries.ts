import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/**
 * The workspace-level self-evolution overview (RUYI-284).
 *
 * The key carries `wsId` even though the endpoint does not take one: a
 * workspace switch must not read the previous workspace's aggregate, exactly
 * as the per-scope dashboards do.
 */
export const selfEvolutionOverviewKeys = {
  all: (wsId: string) => ["self-evolution-overview", wsId] as const,
  root: (wsId: string, days: number) => [...selfEvolutionOverviewKeys.all(wsId), days] as const,
};

export function selfEvolutionOverviewOptions(wsId: string, days = 30) {
  return queryOptions({
    queryKey: selfEvolutionOverviewKeys.root(wsId, days),
    queryFn: () => api.getSelfEvolutionOverview({ days }),
    // The quality half is a daily rollup and the quiz half a periodic sweep;
    // re-reading faster than this only costs requests.
    staleTime: 5 * 60 * 1000,
    enabled: wsId !== "",
  });
}
