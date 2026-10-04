import {
  queryOptions,
  type MutationFunction,
  type QueryClient,
} from "@tanstack/react-query";
import { api } from "../api";
import type { IssueDecision } from "../types";
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

export const answerIssueDecision: MutationFunction<
  IssueDecision,
  { issueId: string; decisionId: string; selectedIndices: number[] }
> = ({ issueId, decisionId, selectedIndices }) =>
  api.answerIssueDecision(issueId, decisionId, selectedIndices);

export const cancelIssueDecision: MutationFunction<
  IssueDecision,
  { issueId: string; decisionId: string }
> = ({ issueId, decisionId }) => api.cancelIssueDecision(issueId, decisionId);
