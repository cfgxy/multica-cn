import {
  queryOptions,
  useQuery,
  type MutationFunction,
  type QueryClient,
} from "@tanstack/react-query";
import { api } from "../api";
import type { IssueDecision, WorkspaceDecisionInbox } from "../types";
import { issueKeys } from "./queries";

// Decision cards (RUYI-345): server state owned by React Query like every
// other issue resource. The list is small (cards are rare) so a plain
// per-issue query with the shared issue staleTime is the whole strategy.

export function issueDecisionsQueryOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.decisions(issueId),
    queryFn: () => api.listIssueDecisions(issueId),
    enabled: !!issueId,
  });
}

/**
 * Workspace decision inbox (RUYI-494). Keys are nested under
 * issueKeys.decisionsAll() so the reconnect invalidation of that prefix
 * (use-realtime-sync.ts) reaches the aggregation for free — a reconnect may
 * have missed decision:updated events from any workspace. Lifecycle events
 * invalidate this subtree explicitly (same file, decision:updated handler).
 */
export const decisionInboxKeys = {
  all: () => [...issueKeys.decisionsAll(), "workspace-inbox"] as const,
  workspace: (workspaceId: string | null | undefined) =>
    [...decisionInboxKeys.all(), workspaceId ?? "none"] as const,
};

export function workspaceDecisionInboxQueryOptions(workspaceId: string | null | undefined) {
  return queryOptions({
    queryKey: decisionInboxKeys.workspace(workspaceId),
    queryFn: async (): Promise<WorkspaceDecisionInbox> => {
      if (!workspaceId) return { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
      return api.listWorkspaceDecisionInbox(workspaceId);
    },
    enabled: !!workspaceId,
  });
}

/**
 * Open-card badge count (nav tabs, RUYI-494): a scalar derived from the same
 * shared query the Decision Center page renders from — one fetch backs both
 * the badge and the list, mirroring the inbox unread pattern.
 */
export function useOpenDecisionCount(workspaceId: string | null | undefined) {
  return useQuery({
    ...workspaceDecisionInboxQueryOptions(workspaceId),
    select: (data) => data.counts.open,
  });
}

/** Replace one card in a cached list (by id) after a mutation or WS event. */
export function patchDecisionInCache(
  qc: QueryClient,
  issueId: string,
  decision: IssueDecision,
): void {
  qc.setQueryData<IssueDecision[]>(issueKeys.decisions(issueId), (prev) => {
    if (!prev) return prev;
    const idx = prev.findIndex((d) => d.id === decision.id);
    if (idx === -1) return prev;
    const next = [...prev];
    next[idx] = decision;
    return next;
  });
}

/**
 * Patch an existing card OR insert a card the cache has never seen, keeping
 * the list ordered by created_at. `patchDecisionInCache` skips unknown ids
 * by design (mutations always act on a fetched card), but a `decision:updated`
 * for a card created while this client had the issue open would silently no-op
 * and the card would stay invisible until the next refetch. Clients that
 * receive lifecycle events for cards they did not create themselves (mobile
 * realtime today) use this upsert instead.
 */
export function upsertDecisionInCache(
  qc: QueryClient,
  issueId: string,
  decision: IssueDecision,
): void {
  qc.setQueryData<IssueDecision[]>(issueKeys.decisions(issueId), (prev) => {
    if (!prev) return prev;
    const idx = prev.findIndex((d) => d.id === decision.id);
    if (idx === -1) {
      return [...prev, decision].sort(
        (a, b) => Date.parse(a.created_at) - Date.parse(b.created_at),
      );
    }
    const next = [...prev];
    next[idx] = decision;
    return next;
  });
}

/**
 * Batch-answer views render their own index letter (OPTION_LETTERS), so a
 * label written under the workspace decision-numbering convention already
 * embeds it ("A：…") and the letter would show twice (RUYI-575). Strip one
 * leading A-Z letter plus one of ： : 、 . when non-empty text follows;
 * anything else (A-type, B超, lowercase, mid-label) renders untouched.
 * Display-only — stored labels stay verbatim.
 */
export function stripDecisionOptionLetterPrefix(label: string): string {
  const match = /^[A-Z][：:、.]\s*(\S.*)$/.exec(label);
  return match?.[1] ?? label;
}

export const answerIssueDecision: MutationFunction<
  IssueDecision,
  { issueId: string; decisionId: string; selectedIndices: number[] }
> = ({ issueId, decisionId, selectedIndices }) =>
  api.answerIssueDecision(issueId, decisionId, selectedIndices);

export const cancelIssueDecision: MutationFunction<
  IssueDecision,
  { issueId: string; decisionId: string }
> = ({ issueId, decisionId }) => api.cancelIssueDecision(issueId, decisionId);
