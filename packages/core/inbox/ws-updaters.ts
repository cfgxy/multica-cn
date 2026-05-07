import type { QueryClient } from "@tanstack/react-query";
import { inboxKeys } from "./queries";
import {
  removeMatchingItems,
  mapAllItems,
  prependToLatestPage,
  type InboxCacheData,
} from "./inbox-cache";
import type { InboxItem, IssueStatus } from "../types";

/**
 * Apply a WS-pushed new inbox item to the paginated cache. Uses surgical
 * prepend (mirrors timeline-cache.prependToLatestPage) instead of a wholesale
 * `invalidateQueries` so the user's scroll position and any in-flight
 * `fetchNextPage` aren't disturbed when a notification arrives.
 *
 * Inbox is single-direction (new items always belong at the top), so unlike
 * the timeline we don't need an `isAtLatest` gate — pages[0] is always the
 * newest page.
 */
export function onInboxNew(qc: QueryClient, wsId: string, item: InboxItem) {
  qc.setQueryData<InboxCacheData>(inboxKeys.list(wsId), (old) =>
    prependToLatestPage(old, item),
  );
  // The badge query is independent of the list query, so a list mutation
  // does not refresh it on its own. Invalidate to trigger a refetch.
  qc.invalidateQueries({ queryKey: inboxKeys.unreadCount(wsId) });
}

export function onInboxIssueStatusChanged(
  qc: QueryClient,
  wsId: string,
  issueId: string,
  status: IssueStatus,
) {
  qc.setQueryData<InboxCacheData>(inboxKeys.list(wsId), (old) =>
    mapAllItems(old, (i) =>
      i.issue_id === issueId ? { ...i, issue_status: status } : i,
    ),
  );
}

// Mirrors the DB-level ON DELETE CASCADE on inbox_item.issue_id: when an issue
// is deleted, all inbox items that referenced it are gone server-side, so drop
// them from the cache too.
export function onInboxIssueDeleted(
  qc: QueryClient,
  wsId: string,
  issueId: string,
) {
  qc.setQueryData<InboxCacheData>(inboxKeys.list(wsId), (old) =>
    removeMatchingItems(old, (i) => i.issue_id === issueId),
  );
  qc.invalidateQueries({ queryKey: inboxKeys.unreadCount(wsId) });
}

export function onInboxInvalidate(qc: QueryClient, wsId: string) {
  qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
  qc.invalidateQueries({ queryKey: inboxKeys.unreadCount(wsId) });
}
