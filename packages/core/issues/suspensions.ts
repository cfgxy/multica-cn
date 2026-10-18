import {
  queryOptions,
  useMutation,
  useQueryClient,
  type MutationFunction,
} from "@tanstack/react-query";
import { api } from "../api";
import type {
  IssueSuspension,
  IssueSuspensionMutation,
  WorkspaceSuspensionList,
} from "../types";
import { issueKeys } from "./queries";

// Issue suspension (RUYI-690): server state owned by React Query like every
// other issue resource. Suspensions are rare per issue, so the strategy is
// the same plain per-issue list the decision cards use, plus a workspace
// aggregation for the 挂起 view.

export function issueSuspensionsQueryOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.suspensions(issueId),
    queryFn: () => api.listIssueSuspensions(issueId),
    enabled: !!issueId,
  });
}

/**
 * Workspace suspension aggregation (RUYI-690 挂起视图). Nested under
 * issueKeys.suspensionsAll() so the reconnect invalidation of that prefix
 * reaches it — a reconnect may have missed `issue_suspension:updated`
 * events from any workspace.
 */
export function workspaceSuspensionsQueryOptions(
  workspaceId: string | null | undefined,
  status?: string,
) {
  return queryOptions({
    queryKey: [...issueKeys.workspaceSuspensions(workspaceId), status ?? "active"],
    queryFn: async (): Promise<WorkspaceSuspensionList> => {
      if (!workspaceId) return { suspensions: [], total: 0 };
      return api.listWorkspaceSuspensions(workspaceId, status ? { status } : undefined);
    },
    enabled: !!workspaceId,
  });
}

export interface SuspendIssueInput {
  issueId: string;
  reason: string;
  note?: string;
  waiting_on_type: "external" | "manual" | "issue" | "decision";
  waiting_on_issue_id?: string;
  waiting_on_decision_id?: string;
  resume_condition?: string;
}

export const suspendIssue: MutationFunction<IssueSuspensionMutation, SuspendIssueInput> = ({
  issueId,
  ...body
}) => api.suspendIssue(issueId, body);

export const resumeIssueSuspension: MutationFunction<
  IssueSuspensionMutation,
  { issueId: string; suspensionId: string; note?: string }
> = ({ issueId, suspensionId, note }) =>
  api.resumeIssueSuspension(issueId, suspensionId, note);

export const cancelIssueSuspension: MutationFunction<
  IssueSuspensionMutation,
  { issueId: string; suspensionId: string; note?: string }
> = ({ issueId, suspensionId, note }) =>
  api.cancelIssueSuspension(issueId, suspensionId, note);

/**
 * Release (resume/cancel) shared mutation: the server returns the updated
 * suspension and the refreshed issue snapshot. Patch the per-issue
 * suspension list in place, then invalidate the suspensions subtree — the
 * workspace aggregation and issue snapshots (suspended_at cleared) rehydrate
 * from their own queries.
 */
export function useReleaseSuspension(mode: "resume" | "cancel") {
  const queryClient = useQueryClient();
  const mutationFn =
    mode === "resume" ? resumeIssueSuspension : cancelIssueSuspension;
  return useMutation({
    mutationFn,
    onSuccess: (result) => {
      const issueId = result.suspension.issue_id;
      queryClient.setQueryData<IssueSuspension[]>(
        issueKeys.suspensions(issueId),
        (prev) => {
          if (!prev) return prev;
          const idx = prev.findIndex((s) => s.id === result.suspension.id);
          if (idx === -1) return prev;
          const next = [...prev];
          next[idx] = result.suspension;
          return next;
        },
      );
      void queryClient.invalidateQueries({
        queryKey: issueKeys.suspensionsAll(),
      });
    },
  });
}
