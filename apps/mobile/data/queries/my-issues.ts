import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

/**
 * "Issues assigned to me" — calls /api/issues with assignee_id filter.
 * Mirror of packages/core/issues/queries.ts myIssueListOptions but mobile
 * v1 fetches the simplest "all assigned to me" slice without the per-status
 * pagination buckets web/desktop use.
 */
export const myIssuesAssignedOptions = (
  wsId: string | null,
  userId: string | null,
) =>
  queryOptions({
    queryKey: ["my-issues", wsId, userId] as const,
    queryFn: async () => {
      const res = await api.listIssues({ assignee_id: userId! });
      return res.issues;
    },
    enabled: !!wsId && !!userId,
  });
