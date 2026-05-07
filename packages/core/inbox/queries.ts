import {
  infiniteQueryOptions,
  queryOptions,
  useQuery,
} from "@tanstack/react-query";
import { api } from "../api";

export const inboxKeys = {
  all: (wsId: string) => ["inbox", wsId] as const,
  list: (wsId: string) => [...inboxKeys.all(wsId), "list"] as const,
  unreadCount: (wsId: string) =>
    [...inboxKeys.all(wsId), "unread-count"] as const,
};

// 50 matches the server's inboxDefaultLimit. Bumping it requires no server
// change (server caps at 100) but should be done with intent — larger pages
// = more items rendered per scroll-to-bottom.
const INBOX_PAGE_SIZE = 50;

/**
 * Cursor-paginated inbox listing. The cache shape is `InfiniteData<InboxListPage>`;
 * consumers flatten via `data?.pages.flatMap(p => p.entries)`.
 *
 * Per-issue dedup happens server-side (SQL `DISTINCT ON (COALESCE(issue_id, id))`)
 * so the client never sees duplicates and never has to reconcile across page
 * boundaries — the failure mode of doing dedup in JS over a paginated list.
 *
 * WS-pushed new items go through `prependToLatestPage` in `inbox-cache.ts`
 * to keep the same dedup invariant after live updates.
 */
export function inboxListInfiniteOptions(wsId: string) {
  return infiniteQueryOptions({
    queryKey: inboxKeys.list(wsId),
    queryFn: ({ pageParam }) =>
      api.listInbox({
        limit: INBOX_PAGE_SIZE,
        ...(pageParam ? { before: pageParam } : {}),
      }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) =>
      last.has_more && last.next_cursor ? last.next_cursor : undefined,
  });
}

/**
 * Unread inbox count for the badge. Calls the dedicated count endpoint
 * (which dedups per-issue server-side, matching the listing) instead of
 * deriving from the loaded list — that derivation can't work once the list
 * is paginated, since the badge would only count the loaded pages.
 */
export function inboxUnreadCountOptions(wsId: string) {
  return queryOptions({
    queryKey: inboxKeys.unreadCount(wsId),
    queryFn: async () => (await api.getUnreadInboxCount()).count,
  });
}

export function useInboxUnreadCount(
  wsId: string | null | undefined,
): number {
  const { data } = useQuery({
    ...inboxUnreadCountOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  return data ?? 0;
}
