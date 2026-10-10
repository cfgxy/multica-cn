import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { SelfEvolutionModelConfigSave } from "../types";
import { selfEvolutionOverviewKeys } from "./overview-queries";

/**
 * Workspace model-service config keys (RUYI-551). The config read feeds the
 * module-config page, the retrospective LLM card, the quality status cards
 * and the overview attention list, so every write invalidates the whole
 * config tree plus the overview aggregate that mirrors `resolved.status`.
 */
export const modelConfigKeys = {
  all: (wsId: string) => ["self-evolution-model-config", wsId] as const,
  root: (wsId: string) => modelConfigKeys.all(wsId),
};

export function selfEvolutionModelConfigOptions(wsId: string) {
  return queryOptions({
    queryKey: modelConfigKeys.root(wsId),
    queryFn: () => api.getSelfEvolutionModelConfig(),
    enabled: wsId !== "",
  });
}

function invalidateConfigReaders(qc: ReturnType<typeof useQueryClient>, wsId: string) {
  qc.invalidateQueries({ queryKey: modelConfigKeys.all(wsId) });
  qc.invalidateQueries({ queryKey: selfEvolutionOverviewKeys.all(wsId) });
}

/** Save the workspace override. A validation failure (422) surfaces through
 * the mutation's error so the card can show the classified, masked reason. */
export function useSaveSelfEvolutionModelConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (save: SelfEvolutionModelConfigSave) => api.putSelfEvolutionModelConfig(save),
    onSuccess: () => invalidateConfigReaders(qc, wsId),
  });
}

/** Drop the workspace override — "restore deploy default". */
export function useRestoreSelfEvolutionModelDefault(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.deleteSelfEvolutionModelConfig(),
    onSuccess: () => invalidateConfigReaders(qc, wsId),
  });
}

/**
 * Re-validate. With no payload this re-checks the stored override (or the
 * deploy default's reachability is NOT probed — the server reports
 * `env` sources as unvalidated); with a payload it previews an unsaved
 * config. Success refreshes the stored validation stamps on a re-check.
 */
export function useValidateSelfEvolutionModelConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (save?: Partial<SelfEvolutionModelConfigSave>) =>
      api.validateSelfEvolutionModelConfig(save),
    onSuccess: (_data, variables) => {
      if (!variables || !(variables.base_url || variables.model || variables.api_key)) {
        invalidateConfigReaders(qc, wsId);
      }
    },
  });
}
