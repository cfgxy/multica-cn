import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { ApiError } from "../api/client";
import type {
  IssueStatus,
  ListIssuesParams,
  ListIssuesCache,
  TimelineEntry,
  TimelinePage,
  TimelinePageParam,
  TimelineV2Page,
} from "../types";
import { BOARD_STATUSES } from "./config";

export const issueKeys = {
  all: (wsId: string) => ["issues", wsId] as const,
  list: (wsId: string) => [...issueKeys.all(wsId), "list"] as const,
  /** All "my issues" queries — use for bulk invalidation. */
  myAll: (wsId: string) => [...issueKeys.all(wsId), "my"] as const,
  /** Per-scope "my issues" list with filter identity baked into the key. */
  myList: (wsId: string, scope: string, filter: MyIssuesFilter) =>
    [...issueKeys.myAll(wsId), scope, filter] as const,
  detail: (wsId: string, id: string) =>
    [...issueKeys.all(wsId), "detail", id] as const,
  children: (wsId: string, id: string) =>
    [...issueKeys.all(wsId), "children", id] as const,
  childProgress: (wsId: string) =>
    [...issueKeys.all(wsId), "child-progress"] as const,
  /**
   * Cursor-paginated timeline cache. Around-mode lookups use a separate cache
   * (keyed by the anchor id) so an Inbox-jump fetch does not pollute the
   * default latest-page cache that the regular issue list path consumes.
   */
  timeline: (issueId: string, around?: string | null) =>
    around
      ? (["issues", "timeline", issueId, "around", around] as const)
      : (["issues", "timeline", issueId] as const),
  reactions: (issueId: string) => ["issues", "reactions", issueId] as const,
  subscribers: (issueId: string) =>
    ["issues", "subscribers", issueId] as const,
  usage: (issueId: string) => ["issues", "usage", issueId] as const,
  /** Per-issue task list (issue-detail Execution log section). */
  tasks: (issueId: string) => ["issues", "tasks", issueId] as const,
  /** Prefix-match key for invalidating tasks across all issues — used by
   *  the global WS task: prefix path so any task lifecycle event refreshes
   *  every per-issue list, regardless of which issue is currently mounted. */
  tasksAll: () => ["issues", "tasks"] as const,
};

export type MyIssuesFilter = Pick<
  ListIssuesParams,
  "assignee_id" | "assignee_ids" | "creator_id" | "project_id"
>;

/** Page size per status column. */
export const ISSUE_PAGE_SIZE = 50;

/** Statuses the issues/my-issues pages paginate. Cancelled is intentionally excluded — it has never been surfaced in the list/board views. */
export const PAGINATED_STATUSES: readonly IssueStatus[] = BOARD_STATUSES;

/** Flatten a bucketed response to a single Issue[] for consumers that want the whole list. */
export function flattenIssueBuckets(data: ListIssuesCache) {
  const out = [];
  for (const status of PAGINATED_STATUSES) {
    const bucket = data.byStatus[status];
    if (bucket) out.push(...bucket.issues);
  }
  return out;
}

async function fetchFirstPages(filter: MyIssuesFilter = {}): Promise<ListIssuesCache> {
  const responses = await Promise.all(
    PAGINATED_STATUSES.map((status) =>
      api.listIssues({ status, limit: ISSUE_PAGE_SIZE, offset: 0, ...filter }),
    ),
  );
  const byStatus: ListIssuesCache["byStatus"] = {};
  PAGINATED_STATUSES.forEach((status, i) => {
    const res = responses[i]!;
    byStatus[status] = { issues: res.issues, total: res.total };
  });
  return { byStatus };
}

/**
 * CACHE SHAPE NOTE: The raw cache stores {@link ListIssuesCache} (buckets keyed
 * by status, each with `{ issues, total }`), and `select` flattens it to
 * `Issue[]` for consumers. Mutations and ws-updaters must use
 * `setQueryData<ListIssuesCache>(...)` and preserve the byStatus shape.
 *
 * Fetches the first page of each paginated status in parallel. Use
 * {@link useLoadMoreByStatus} to paginate a specific status into the cache.
 */
export function issueListOptions(wsId: string) {
  return queryOptions({
    queryKey: issueKeys.list(wsId),
    queryFn: () => fetchFirstPages(),
    select: flattenIssueBuckets,
  });
}

/**
 * Server-filtered issue list for the My Issues page.
 * Each scope gets its own cache entry so switching tabs is instant after first load.
 */
export function myIssueListOptions(
  wsId: string,
  scope: string,
  filter: MyIssuesFilter,
) {
  return queryOptions({
    queryKey: issueKeys.myList(wsId, scope, filter),
    queryFn: () => fetchFirstPages(filter),
    select: flattenIssueBuckets,
  });
}

export function issueDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: issueKeys.detail(wsId, id),
    queryFn: () => api.getIssue(id),
  });
}

export function childIssueProgressOptions(wsId: string) {
  return queryOptions({
    queryKey: issueKeys.childProgress(wsId),
    queryFn: () => api.getChildIssueProgress(),
    select: (data) => {
      const map = new Map<string, { done: number; total: number }>();
      for (const entry of data.progress) {
        map.set(entry.parent_issue_id, { done: entry.done, total: entry.total });
      }
      return map;
    },
  });
}

export function childIssuesOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: issueKeys.children(wsId, id),
    queryFn: () => api.listChildIssues(id).then((r) => r.issues),
  });
}

/**
 * Translate a V2 (comment-anchored) timeline page into the V1-compatible
 * cache shape. Comments and activities are merged DESC by (created_at, id);
 * V2-specific signals (target, activity_truncated_count) ride along on the
 * optional fields of TimelinePage so existing consumers keep working.
 *
 * Why translate instead of switching the cache to V2's two-array shape?
 * The hook (`useIssueTimeline`) and the cache helpers (`mapAllEntries`,
 * `prependToLatestPage`, etc.) treat the timeline as a single mixed list,
 * which is what the UI actually renders. Forking those for V2 would double
 * the surface area for very little gain — the comment/activity split is a
 * pagination concern, not a rendering concern.
 */
function v2PageToV1(v2: TimelineV2Page): TimelinePage {
  const entries: TimelineEntry[] = [...v2.comments, ...v2.activities].sort(
    (a, b) => {
      if (a.created_at !== b.created_at) {
        return b.created_at.localeCompare(a.created_at);
      }
      return b.id.localeCompare(a.id);
    },
  );
  let target_index: number | undefined;
  if (v2.target) {
    const idx = entries.findIndex((e) => e.id === v2.target!.id);
    if (idx >= 0) target_index = idx;
  }
  return {
    entries,
    next_cursor: v2.next_cursor,
    prev_cursor: v2.prev_cursor,
    has_more_before: v2.has_more_before,
    has_more_after: v2.has_more_after,
    target_index,
    target: v2.target ?? null,
    activity_truncated_count: v2.activity_truncated_count ?? null,
  };
}

/**
 * Infinite-query options for the V2 (comment-anchored) timeline. The first
 * page is either the latest comments + their activity window (no `around`)
 * or a window centered on the given comment/activity id (Inbox jump path).
 * `getNextPageParam` walks older; `getPreviousPageParam` walks newer.
 *
 * Cursors emitted by V2 endpoints encode a comment's (created_at, id) and
 * are not interchangeable with V1 cursors — keep this options object the
 * single entry point so the cursor never escapes to a V1 listTimeline call.
 */
export function issueTimelineInfiniteOptions(
  issueId: string,
  around?: string | null,
) {
  return infiniteQueryOptions<
    TimelinePage,
    Error,
    { pages: TimelinePage[]; pageParams: TimelinePageParam[] },
    readonly unknown[],
    TimelinePageParam
  >({
    queryKey: issueKeys.timeline(issueId, around ?? null),
    initialPageParam: around
      ? ({ mode: "around", id: around } as TimelinePageParam)
      : ({ mode: "latest" } as TimelinePageParam),
    queryFn: async ({ pageParam }) => {
      try {
        const v2 = await api.listTimelineV2(issueId, pageParam);
        return v2PageToV1(v2);
      } catch (err) {
        // Around-mode anchor was deleted between when the inbox notification
        // was dispatched and when the user clicked it (or the link was hand-
        // crafted with a stale id). Server returns 404 for the missing
        // entry; without this fallback the issue detail page would render
        // an entirely empty timeline even though the issue still has other
        // comments and activities. Drop the anchor and refetch as latest —
        // user lands on the live tail rather than blank space.
        if (
          pageParam.mode === "around" &&
          err instanceof ApiError &&
          err.status === 404
        ) {
          const v2 = await api.listTimelineV2(issueId, { mode: "latest" });
          return v2PageToV1(v2);
        }
        throw err;
      }
    },
    // Walk older: append a page below the current oldest (last entry of the
    // last loaded page). undefined = no more older entries.
    getNextPageParam: (lastPage) =>
      lastPage.has_more_before && lastPage.next_cursor
        ? ({ mode: "before", cursor: lastPage.next_cursor } as TimelinePageParam)
        : undefined,
    // Walk newer: prepend a page above the current newest (first entry of the
    // first loaded page). undefined = at the latest, no newer to fetch.
    getPreviousPageParam: (firstPage) =>
      firstPage.has_more_after && firstPage.prev_cursor
        ? ({ mode: "after", cursor: firstPage.prev_cursor } as TimelinePageParam)
        : undefined,
  });
}

export function issueReactionsOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.reactions(issueId),
    queryFn: async () => {
      const issue = await api.getIssue(issueId);
      return issue.reactions ?? [];
    },
  });
}

export function issueSubscribersOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.subscribers(issueId),
    queryFn: () => api.listIssueSubscribers(issueId),
  });
}

export function issueUsageOptions(issueId: string) {
  return queryOptions({
    queryKey: issueKeys.usage(issueId),
    queryFn: () => api.getIssueUsage(issueId),
  });
}
