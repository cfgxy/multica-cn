import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { inboxKeys } from "./queries";
import {
  mapAllItems,
  removeMatchingItems,
  type InboxCacheData,
} from "./inbox-cache";
import { useWorkspaceId } from "../hooks";

// Why these mutations no longer invalidate the LIST query:
//
// useInfiniteQuery refetches every loaded page with its cached pageParam on
// invalidate. After a row is archived, page 0 (no cursor) shifts up to
// include items previously on page 1, while page 1 still uses a cursor
// pointing at the now-archived row — so the same item appears on both
// pages. The old client-side `deduplicateInboxItems` masked this; deleting
// it surfaced the bug.
//
// All mutations whose effect the client can predict EXACTLY (mark-read /
// archive-single / mark-all-read / archive-all / archive-all-read) apply
// optimistically and skip the list invalidate — the local cache is already
// correct. Only the unread-count query is invalidated (it's a single
// non-paginated query, immune to the cross-page issue).
//
// `useArchiveCompletedInbox` is the lone exception: it filters by
// issue.status server-side, so the client can't enumerate which inbox
// items are affected. It still invalidates the list and inherits the
// cross-page-duplicate risk in the rare case a user with deeply scrolled
// pagination triggers it. Acceptable trade-off — same behavior as the
// timeline cache today.

function invalidateUnreadCount(
  qc: ReturnType<typeof useQueryClient>,
  wsId: string,
) {
  qc.invalidateQueries({ queryKey: inboxKeys.unreadCount(wsId) });
}

export function useMarkInboxRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.markInboxRead(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: inboxKeys.list(wsId) });
      const prev = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
      qc.setQueryData<InboxCacheData>(inboxKeys.list(wsId), (old) =>
        mapAllItems(old, (item) =>
          item.id === id ? { ...item, read: true } : item,
        ),
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => invalidateUnreadCount(qc, wsId),
  });
}

export function useArchiveInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.archiveInbox(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: inboxKeys.list(wsId) });
      const prev = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
      // Find the target so we can issue-archive (removing siblings on the
      // same issue) — server does the same in ArchiveInboxItem when the
      // issue_id is set.
      const target = prev?.pages
        .flatMap((p) => p.entries)
        .find((i) => i.id === id);
      const issueId = target?.issue_id ?? null;
      qc.setQueryData<InboxCacheData>(inboxKeys.list(wsId), (old) =>
        removeMatchingItems(old, (item) =>
          item.id === id || (issueId !== null && item.issue_id === issueId),
        ),
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => invalidateUnreadCount(qc, wsId),
  });
}

export function useMarkAllInboxRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.markAllInboxRead(),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: inboxKeys.list(wsId) });
      const prev = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
      qc.setQueryData<InboxCacheData>(inboxKeys.list(wsId), (old) =>
        mapAllItems(old, (item) =>
          !item.archived ? { ...item, read: true } : item,
        ),
      );
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => invalidateUnreadCount(qc, wsId),
  });
}

export function useArchiveAllInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveAllInbox(),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: inboxKeys.list(wsId) });
      const prev = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
      // Wipe every loaded page — server is archiving everything.
      qc.setQueryData<InboxCacheData>(inboxKeys.list(wsId), (old) =>
        removeMatchingItems(old, () => true),
      );
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => invalidateUnreadCount(qc, wsId),
  });
}

export function useArchiveAllReadInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveAllReadInbox(),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: inboxKeys.list(wsId) });
      const prev = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
      qc.setQueryData<InboxCacheData>(inboxKeys.list(wsId), (old) =>
        removeMatchingItems(old, (item) => item.read),
      );
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => invalidateUnreadCount(qc, wsId),
  });
}

export function useArchiveCompletedInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    // Server-side filter on issue.status — the client can't enumerate
    // which inbox items are affected, so this is the only mutation that
    // still invalidates the list. Inherits the cross-page duplicate
    // risk on deeply paginated caches; same as timeline today.
    mutationFn: () => api.archiveCompletedInbox(),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
      invalidateUnreadCount(qc, wsId);
    },
  });
}
