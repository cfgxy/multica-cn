import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { WorkspacePromQLRuleSave } from "../types";

export const workspacePromQLRuleKeys = {
  all: (wsId: string) => ["workspace-promql-rules", wsId] as const,
  root: (wsId: string) => workspacePromQLRuleKeys.all(wsId),
};

export function workspacePromQLRulesOptions(wsId: string) {
  return {
    queryKey: workspacePromQLRuleKeys.root(wsId),
    queryFn: () => api.getWorkspacePromQLRules(),
    enabled: wsId !== "",
  };
}

export function useSaveWorkspacePromQLRules(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (rules: WorkspacePromQLRuleSave[]) =>
      api.putWorkspacePromQLRules(rules),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: workspacePromQLRuleKeys.all(wsId) }),
  });
}

export function usePreviewWorkspacePromQLRule() {
  return useMutation({
    mutationFn: (rule: WorkspacePromQLRuleSave) =>
      api.previewWorkspacePromQLRule(rule),
  });
}
