import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const clauseGovernanceKeys = {
  all: () => ["clause-governance"] as const,
  overview: (wsId: string, days: number) => ["clause-governance", "overview", wsId, days] as const,
  detail: (wsId: string, clauseId: string, days: number) =>
    ["clause-governance", "detail", wsId, clauseId, days] as const,
  reconciliation: (wsId: string, days: number) =>
    ["clause-governance", "reconciliation", wsId, days] as const,
};

export function clauseGovernanceOverviewOptions(wsId: string, days: number) {
  return queryOptions({
    queryKey: clauseGovernanceKeys.overview(wsId, days),
    queryFn: () => api.getClauseGovernanceOverview(days),
    staleTime: 5 * 60 * 1000,
    enabled: wsId !== "",
  });
}

export function clauseGovernanceDetailOptions(wsId: string, clauseId: string, days: number) {
  return queryOptions({
    queryKey: clauseGovernanceKeys.detail(wsId, clauseId, days),
    queryFn: () => api.getClauseGovernanceDetail(clauseId, days),
    staleTime: 5 * 60 * 1000,
    enabled: wsId !== "" && clauseId !== "",
  });
}

export function clauseGovernanceReconciliationOptions(wsId: string, days: number) {
  return queryOptions({
    queryKey: clauseGovernanceKeys.reconciliation(wsId, days),
    queryFn: () => api.getClauseGovernanceReconciliation(days),
    staleTime: 5 * 60 * 1000,
    enabled: wsId !== "",
  });
}
