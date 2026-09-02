/**
 * Mobile chat mutations — create / rename / pin / archive / delete session,
 * mark session read.
 *
 * Send-message is NOT a mutation: the chat screen runs a hand-written
 * optimistic burst (seed messages cache → seed pendingTask cache → flip
 * activeSession → POST → patch with real task_id) that doesn't map cleanly
 * onto useMutation. See the chat tab screen for the send path.
 *
 * Mirrors the optimistic-update + rollback + onSettled-invalidate pattern
 * of data/mutations/inbox.ts and web's packages/core/chat/mutations.ts.
 * The session-management writes (update / pin / archive) mirror web's
 * useUpdateChatSession / useSetChatSessionPinned / useSetChatSessionArchived
 * optimistic patches one-to-one, including the pin/archive re-sort via
 * sortChatSessions (mirror in data/queries/chat.ts) and archive's unread
 * zeroing (MUL-4360 mirror).
 */
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ChatSession } from "@multica/core/types";
import { api } from "@/data/api";
import { useWorkspaceStore } from "@/data/workspace-store";
import { chatKeys, sortChatSessions } from "@/data/queries/chat";

export function useCreateChatSession() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (data: { agent_id: string; title?: string }) =>
      api.createChatSession(data),
    onSettled: () => {
      // Optimistic prepend isn't done here — the chat screen seeds caches
      // synchronously around its send burst and uses the returned session
      // id directly. The invalidate ensures the dropdown picks up the new
      // row (and any has_unread / title server defaults) without a refetch
      // race on switch.
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
    },
  });
}

export function useDeleteChatSession() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (id: string) => api.deleteChatSession(id),
    onMutate: async (id) => {
      const key = chatKeys.sessions(wsId);
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<ChatSession[]>(key);
      qc.setQueryData<ChatSession[]>(key, (old) =>
        old ? old.filter((s) => s.id !== id) : old,
      );
      return { prev, key };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(ctx.key, ctx.prev);
    },
    onSettled: (_data, _err, id) => {
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
      // Detail-side caches the screen may still hold for this id.
      qc.removeQueries({ queryKey: chatKeys.messages(id) });
      qc.removeQueries({ queryKey: chatKeys.pendingTask(id) });
    },
  });
}

export function useUpdateChatSession() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (data: { sessionId: string; title: string }) =>
      api.updateChatSession(data.sessionId, { title: data.title }),
    onMutate: async ({ sessionId, title }) => {
      const key = chatKeys.sessions(wsId);
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<ChatSession[]>(key);
      qc.setQueryData<ChatSession[]>(key, (old) =>
        old?.map((s) => (s.id === sessionId ? { ...s, title } : s)),
      );
      return { prev, key };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(ctx.key, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
    },
  });
}

export function useSetChatSessionPinned() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (data: { sessionId: string; pinned: boolean }) =>
      api.setChatSessionPinned(data.sessionId, data.pinned),
    onMutate: async ({ sessionId, pinned }) => {
      const key = chatKeys.sessions(wsId);
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<ChatSession[]>(key);
      // Re-sort (pinned first, then activity) so the row jumps to / from the
      // top instantly — same shape as web's useSetChatSessionPinned.
      qc.setQueryData<ChatSession[]>(key, (old) =>
        old &&
        sortChatSessions(
          old.map((s) => (s.id === sessionId ? { ...s, pinned } : s)),
        ),
      );
      return { prev, key };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(ctx.key, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
    },
  });
}

export function useSetChatSessionArchived() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (data: { sessionId: string; archived: boolean }) =>
      api.setChatSessionArchived(data.sessionId, data.archived),
    onMutate: async ({ sessionId, archived }) => {
      const key = chatKeys.sessions(wsId);
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<ChatSession[]>(key);
      // Flip status + bump updated_at so the row re-sorts into its new view;
      // archiving also zeroes unread locally so every badge drops it in the
      // same frame the row moves (server forces unread 0 for archived rows,
      // MUL-4360). Unarchive does NOT restore a count — the settle refetch
      // brings the true unread state back. Mirrors web's
      // useSetChatSessionArchived.
      const nowIso = new Date().toISOString();
      qc.setQueryData<ChatSession[]>(key, (old) =>
        old &&
        sortChatSessions(
          old.map((s) =>
            s.id === sessionId
              ? {
                  ...s,
                  status: archived ? "archived" : "active",
                  updated_at: nowIso,
                  ...(archived ? { unread_count: 0, has_unread: false } : {}),
                }
              : s,
          ),
        ),
      );
      return { prev, key };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(ctx.key, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
    },
  });
}

export function useMarkChatSessionRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (sessionId: string) => api.markChatSessionRead(sessionId),
    onMutate: async (sessionId) => {
      const key = chatKeys.sessions(wsId);
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<ChatSession[]>(key);
      // Zero unread_count together with has_unread — the tab badge sums
      // unread_count (see lib/unread-counts.ts), so clearing only the flag
      // would leave a stale badge until the settle refetch. Mirrors web's
      // useMarkChatSessionRead in packages/core/chat/mutations.ts.
      qc.setQueryData<ChatSession[]>(key, (old) =>
        old?.map((s) =>
          s.id === sessionId
            ? { ...s, has_unread: false, unread_count: 0 }
            : s,
        ),
      );
      return { prev, key };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(ctx.key, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
    },
  });
}
