import {
  infiniteQueryOptions,
  queryOptions,
  useQuery,
  type MutationFunction,
  type QueryClient,
} from "@tanstack/react-query";
import { api } from "../api";
import type {
  IssueDecision,
  IssueDecisionStatus,
  WorkspaceDecisionInbox,
} from "../types";
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
/** Filter window of a decision-inbox list call: one card status (a 1:1 real
 * state — request lifecycle statuses are never folded onto a card) plus the
 * RUYI-864 raw keyword. Keys nest under the workspace prefix so a
 * reconnect's invalidation of decisionInboxKeys.all() reaches every filter
 * window, not just the default one. */
export interface WorkspaceDecisionInboxFilters {
  status?: IssueDecisionStatus;
  /** Raw keyword; LIKE wildcards are escaped server-side. */
  q?: string;
  /** Page size for a keyset walk (server default 200, max 500). */
  limit?: number;
}

export const decisionInboxKeys = {
  all: () => [...issueKeys.decisionsAll(), "workspace-inbox"] as const,
  workspace: (workspaceId: string | null | undefined) =>
    [...decisionInboxKeys.all(), workspaceId ?? "none"] as const,
  /** One filter window's list (RUYI-864). */
  inbox: (
    workspaceId: string | null | undefined,
    filters: WorkspaceDecisionInboxFilters,
  ) => [...decisionInboxKeys.workspace(workspaceId), filters] as const,
};

export function workspaceDecisionInboxQueryOptions(
  workspaceId: string | null | undefined,
  filters?: WorkspaceDecisionInboxFilters,
) {
  return queryOptions({
    queryKey: filters
      ? decisionInboxKeys.inbox(workspaceId, filters)
      : decisionInboxKeys.workspace(workspaceId),
    queryFn: async (): Promise<WorkspaceDecisionInbox> => {
      if (!workspaceId)
        return {
          items: [],
          counts: { open: 0, answered: 0, cancelled: 0 },
          next_cursor: null,
          next_cursor_id: null,
        };
      return api.listWorkspaceDecisionInbox(workspaceId, filters);
    },
    enabled: !!workspaceId,
  });
}

/**
 * RUYI-864 keyset pagination of the decision list — the same two-field
 * cursor shape as the audit-events page options (the cursor pair must
 * travel together; a boundary that splits them would duplicate or skip the
 * card that shares a created_at). The counts in every page are scoped to
 * the filter window, so a section header stays true even after paging;
 * the badge keeps using the unfiltered default query (limit-immune).
 */
export function workspaceDecisionInboxPageOptions(
  workspaceId: string | null | undefined,
  filters: WorkspaceDecisionInboxFilters = {},
) {
  return infiniteQueryOptions({
    queryKey: decisionInboxKeys.inbox(workspaceId, filters),
    initialPageParam: null as { after_created_at: string; after_id: string } | null,
    queryFn: async ({ pageParam }): Promise<WorkspaceDecisionInbox> => {
      if (!workspaceId)
        return {
          items: [],
          counts: { open: 0, answered: 0, cancelled: 0 },
          next_cursor: null,
          next_cursor_id: null,
        };
      return api.listWorkspaceDecisionInbox(workspaceId, {
        ...filters,
        ...(pageParam ?? {}),
      });
    },
    getNextPageParam: (lastPage) =>
      lastPage.next_cursor && lastPage.next_cursor_id
        ? {
            after_created_at: lastPage.next_cursor,
            after_id: lastPage.next_cursor_id,
          }
        : undefined,
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
 * embeds it and the letter would show twice (RUYI-575). Strip one leading
 * A-Z letter plus its separator — "：:、." or a space, half- or full-width
 * (RUYI-620) — when non-empty text follows; anything else (A股龙头, AB 测试,
 * lowercase, mid-label) renders untouched.
 * Display-only — stored labels stay verbatim.
 */
export function stripDecisionOptionLetterPrefix(label: string): string {
  const match = /^[A-Z]\s*(?:[：:、.．]|\s)\s*(\S.*)$/.exec(label);
  return match?.[1] ?? label;
}

/**
 * The option control renders its own recommended badge (recommended_indices),
 * so a trailing "（推荐）"/"(推荐)" in the label would double it (RUYI-620).
 * Strip it only when it terminates the label — repeated trailing markers
 * strip together; mid-label or leading occurrences are content.
 * Display-only — stored labels stay verbatim.
 */
export function stripDecisionOptionRecommendedSuffix(label: string): string {
  return label.replace(/(?:\s*[（(]\s*推荐\s*[)）])+\s*$/, "");
}

/**
 * The single display helper for decision option labels across render
 * surfaces (issue card, batch bar, and their mobile copies): strips the
 * embedded letter prefix and the trailing recommended marker so the
 * control's own index letter and badge render exactly once (RUYI-620).
 */
export function decisionOptionDisplayLabel(label: string): string {
  return stripDecisionOptionLetterPrefix(stripDecisionOptionRecommendedSuffix(label));
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
