import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { UpdateOuterLoopPlanInput } from "./outerloop";

export const outerLoopPlanKey = (wsId: string, issueId: string) =>
  ["workspaces", wsId, "issues", issueId, "outerloop-plan"] as const;

export const outerLoopPlanOptions = (wsId: string, issueId: string) =>
  queryOptions({
    queryKey: outerLoopPlanKey(wsId, issueId),
    queryFn: () => api.getOuterLoopPlan(issueId),
    enabled: wsId !== "" && issueId !== "",
  });

function useInvalidateOuterLoopPlan(wsId: string, issueId: string) {
  const client = useQueryClient();
  return () => client.invalidateQueries({ queryKey: outerLoopPlanKey(wsId, issueId) });
}

export function useUpdateOuterLoopPlan(wsId: string, issueId: string) {
  const invalidate = useInvalidateOuterLoopPlan(wsId, issueId);
  return useMutation({
    mutationFn: (input: UpdateOuterLoopPlanInput) => api.updateOuterLoopPlan(issueId, input),
    onSuccess: invalidate,
  });
}

export function useFireOuterLoopPlan(wsId: string, issueId: string) {
  const invalidate = useInvalidateOuterLoopPlan(wsId, issueId);
  return useMutation({ mutationFn: () => api.fireOuterLoopPlan(issueId), onSuccess: invalidate });
}

export function useStopOuterLoopPlan(wsId: string, issueId: string) {
  const invalidate = useInvalidateOuterLoopPlan(wsId, issueId);
  return useMutation({ mutationFn: () => api.stopOuterLoopPlan(issueId), onSuccess: invalidate });
}
