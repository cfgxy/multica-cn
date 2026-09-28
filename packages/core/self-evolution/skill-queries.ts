import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";

export const skillEvolutionKeys = {
  all: (wsId: string) => ["skill-evolution", wsId] as const,
  versions: (wsId: string, skillId: string) => [...skillEvolutionKeys.all(wsId), skillId, "versions"] as const,
  version: (wsId: string, skillId: string, number: number) => [...skillEvolutionKeys.all(wsId), skillId, "version", number] as const,
  usage: (wsId: string, skillId: string) => [...skillEvolutionKeys.all(wsId), skillId, "usage"] as const,
};

export function skillVersionsOptions(wsId: string, skillId: string) {
  return queryOptions({
    queryKey: skillEvolutionKeys.versions(wsId, skillId),
    queryFn: () => api.listSkillVersions(skillId),
    enabled: !!wsId && !!skillId,
  });
}

export function skillVersionOptions(wsId: string, skillId: string, number: number) {
  return queryOptions({
    queryKey: skillEvolutionKeys.version(wsId, skillId, number),
    queryFn: () => api.getSkillVersion(skillId, number),
    enabled: !!wsId && !!skillId && number > 0,
  });
}

export function skillUsageOptions(wsId: string, skillId: string) {
  return queryOptions({
    queryKey: skillEvolutionKeys.usage(wsId, skillId),
    queryFn: () => api.getSkillUsage(skillId),
    enabled: !!wsId && !!skillId,
  });
}

export function useRestoreSkillVersion(wsId: string, skillId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (number: number) => api.restoreSkillVersion(skillId, number),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: skillEvolutionKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
    },
  });
}
