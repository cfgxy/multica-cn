import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/** The (scope_type, scope_id) container a surface's views live in. */
export interface IssueViewScope {
  scope_type: "workspace" | "my" | "project";
  scope_id?: string | null;
}

export const issueViewKeys = {
  all: (wsId: string) => ["issue-views", wsId] as const,
  list: (wsId: string, scope: IssueViewScope) =>
    [...issueViewKeys.all(wsId), scope.scope_type, scope.scope_id ?? null] as const,
};

export function issueViewListOptions(wsId: string, scope: IssueViewScope) {
  return queryOptions({
    queryKey: issueViewKeys.list(wsId, scope),
    queryFn: () => api.listIssueViews(scope),
    enabled: !!wsId,
  });
}
