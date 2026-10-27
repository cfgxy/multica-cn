import type { IssueDecision } from "@multica/core/types";

export function getBatchDecisionCandidates(decisions: IssueDecision[]): IssueDecision[] {
  return decisions
    .filter((decision) => decision.status === "open" && decision.decision_kind !== "authorization")
    .slice()
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
}
