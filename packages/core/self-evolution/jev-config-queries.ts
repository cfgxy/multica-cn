import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { SelfEvolutionJevConfigSave } from "../types";

export const jevConfigKeys = {
  all: (wsId: string) => ["self-evolution-jev-config", wsId] as const,
  root: (wsId: string) => jevConfigKeys.all(wsId),
};

export function selfEvolutionJevConfigOptions(wsId: string) {
  return queryOptions({
    queryKey: jevConfigKeys.root(wsId),
    queryFn: () => api.getSelfEvolutionJevConfig(),
    enabled: wsId !== "",
  });
}

function invalidateJevConfig(qc: ReturnType<typeof useQueryClient>, wsId: string) {
  qc.invalidateQueries({ queryKey: jevConfigKeys.all(wsId) });
}

export function useSaveSelfEvolutionJevConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (save: SelfEvolutionJevConfigSave) => api.putSelfEvolutionJevConfig(save),
    onSuccess: () => invalidateJevConfig(qc, wsId),
  });
}

export function useDeleteSelfEvolutionJevConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.deleteSelfEvolutionJevConfig(),
    onSuccess: () => invalidateJevConfig(qc, wsId),
  });
}

export function useRecheckSelfEvolutionJevConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.recheckSelfEvolutionJevConfig(),
    onSuccess: () => invalidateJevConfig(qc, wsId),
  });
}
