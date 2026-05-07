import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { inboxKeys } from "./queries";
import {
  filterAllItems,
  mapAllItems,
  type InboxCacheData,
} from "./inbox-cache";
import { useWorkspaceId } from "../hooks";

// Each mutation invalidates the unread-count query so the badge stays in
// sync with the optimistic list update. The badge derives from a separate
// endpoint now (see useInboxUnreadCount) so list-only invalidation isn't
// enough.
function invalidateInbox(
  qc: ReturnType<typeof useQueryClient>,
  wsId: string,
) {
  qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
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
    onSettled: () => invalidateInbox(qc, wsId),
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
        filterAllItems(old, (item) =>
          item.id === id || (issueId !== null && item.issue_id === issueId),
        ),
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => invalidateInbox(qc, wsId),
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
    onSettled: () => invalidateInbox(qc, wsId),
  });
}

export function useArchiveAllInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveAllInbox(),
    onSettled: () => invalidateInbox(qc, wsId),
  });
}

export function useArchiveAllReadInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveAllReadInbox(),
    onSettled: () => invalidateInbox(qc, wsId),
  });
}

export function useArchiveCompletedInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveCompletedInbox(),
    onSettled: () => invalidateInbox(qc, wsId),
  });
}
