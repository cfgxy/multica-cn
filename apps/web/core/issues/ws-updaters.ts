import type { QueryClient } from "@tanstack/react-query";
import { issueKeys } from "./queries";
import type { Issue } from "@/shared/types";
import type { ListIssuesResponse } from "@/shared/types";

export function onIssueCreated(
  qc: QueryClient,
  wsId: string,
  issue: Issue,
) {
  qc.setQueryData<ListIssuesResponse>(issueKeys.list(wsId), (old) => {
    if (!old || old.issues.some((i) => i.id === issue.id)) return old;
    return {
      ...old,
      issues: [...old.issues, issue],
      total: old.total + 1,
      doneTotal: (old.doneTotal ?? 0) + (issue.status === "done" ? 1 : 0),
    };
  });
}

export function onIssueUpdated(
  qc: QueryClient,
  wsId: string,
  issue: Partial<Issue> & { id: string },
) {
  qc.setQueryData<ListIssuesResponse>(issueKeys.list(wsId), (old) => {
    if (!old) return old;
    const prev = old.issues.find((i) => i.id === issue.id);
    const wasDone = prev?.status === "done";
    const isDone = issue.status === "done";
    // Only adjust doneTotal when status field is present and actually changed
    let doneDelta = 0;
    if (issue.status !== undefined) {
      if (!wasDone && isDone) doneDelta = 1;
      else if (wasDone && !isDone) doneDelta = -1;
    }
    return {
      ...old,
      issues: old.issues.map((i) =>
        i.id === issue.id ? { ...i, ...issue } : i,
      ),
      doneTotal: (old.doneTotal ?? 0) + doneDelta,
    };
  });
  qc.setQueryData<Issue>(issueKeys.detail(wsId, issue.id), (old) =>
    old ? { ...old, ...issue } : old,
  );
}

export function onIssueDeleted(
  qc: QueryClient,
  wsId: string,
  issueId: string,
) {
  qc.setQueryData<ListIssuesResponse>(issueKeys.list(wsId), (old) => {
    if (!old) return old;
    const deleted = old.issues.find((i) => i.id === issueId);
    return {
      ...old,
      issues: old.issues.filter((i) => i.id !== issueId),
      total: old.total - 1,
      doneTotal: (old.doneTotal ?? 0) - (deleted?.status === "done" ? 1 : 0),
    };
  });
  qc.removeQueries({ queryKey: issueKeys.detail(wsId, issueId) });
  qc.removeQueries({ queryKey: issueKeys.timeline(issueId) });
  qc.removeQueries({ queryKey: issueKeys.reactions(issueId) });
  qc.removeQueries({ queryKey: issueKeys.subscribers(issueId) });
}
