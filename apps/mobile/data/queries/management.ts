import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const managementKeys = {
  workspace: (wsId: string | null) => ["workspace-management", wsId] as const,
  selfEvolution: (wsId: string | null) => ["self-evolution", wsId] as const,
  proposals: (wsId: string | null) => ["prompt-proposals", wsId] as const,
  clauses: (wsId: string | null) => ["clause-governance", wsId] as const,
  autopilots: (wsId: string | null) => ["autopilots", wsId] as const,
  retrospectives: (wsId: string | null) => ["retrospective-runs", wsId] as const,
};

export const selfEvolutionOverviewOptions = (wsId: string | null) => queryOptions({
  queryKey: managementKeys.selfEvolution(wsId),
  queryFn: ({ signal }) => api.getSelfEvolutionOverview({ signal }),
  enabled: !!wsId,
});

export const promptProposalsOptions = (wsId: string | null) => queryOptions({
  queryKey: managementKeys.proposals(wsId),
  queryFn: ({ signal }) => api.listPromptProposals({ signal }),
  enabled: !!wsId,
});

export const clauseGovernanceOptions = (wsId: string | null) => queryOptions({
  queryKey: managementKeys.clauses(wsId),
  queryFn: ({ signal }) => api.getClauseGovernanceOverview({ signal }),
  enabled: !!wsId,
});

export const autopilotsOptions = (wsId: string | null) => queryOptions({
  queryKey: managementKeys.autopilots(wsId),
  queryFn: ({ signal }) => api.listAutopilots({ signal }),
  enabled: !!wsId,
});

export const retrospectiveRunsOptions = (wsId: string | null) => queryOptions({
  queryKey: managementKeys.retrospectives(wsId),
  queryFn: ({ signal }) => api.listRetrospectiveRuns({ signal }),
  enabled: !!wsId,
});
