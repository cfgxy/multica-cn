/**
 * Mobile inbox mutations. Mirrors the optimistic-update + invalidate pattern
 * of packages/core/inbox/mutations.ts — written here in mobile-owned code
 * per Sharing Principles (no runtime imports from @multica/core mutations).
 *
 * Behavioral parity:
 *   - mark-read: flip `read` to true locally; rollback on error; settle invalidate.
 *   - archive single: flip `archived` to true on the item AND on every other
 *     inbox row that shares the same `issue_id` (web does the same — see
 *     packages/core/inbox/mutations.ts:37-46). Visually the row disappears
 *     because `deduplicateInboxItems` (apps/mobile/lib/inbox-display.ts)
 *     filters archived items out before render.
 *   - archive batch (all / all-read / completed): no optimistic patch — the
 *     row predicates depend on server-side state (e.g. issue.status="done"
 *     isn't carried on every row, and mobile shouldn't re-derive the filter).
 *     Just invalidate on settle. Matches web.
 */
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { InboxItem } from "@multica/core/types";
import { api } from "@/data/api";
import { useWorkspaceStore } from "@/data/workspace-store";

export function useMarkInboxRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (id: string) => api.markInboxRead(id),
    onMutate: async (id) => {
      const key = ["inbox", wsId] as const;
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<InboxItem[]>(key);
      qc.setQueryData<InboxItem[]>(key, (old) =>
        old?.map((item) => (item.id === id ? { ...item, read: true } : item)),
      );
      return { prev, key };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(ctx.key, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["inbox", wsId] });
    },
  });
}

export function useArchiveInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (id: string) => api.archiveInbox(id),
    onMutate: async (id) => {
      const key = ["inbox", wsId] as const;
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<InboxItem[]>(key);
      // Match web: archive every row that shares the same issue_id — the
      // single archive endpoint archives all sibling rows server-side too
      // (`server/internal/queries/inbox.sql` UPDATE … WHERE issue_id = ?).
      // Patching only the tapped row would let dedup'd siblings briefly
      // resurface between the request and the WS invalidate.
      const target = prev?.find((i) => i.id === id);
      const issueId = target?.issue_id ?? null;
      qc.setQueryData<InboxItem[]>(key, (old) =>
        old?.map((item) =>
          item.id === id || (issueId && item.issue_id === issueId)
            ? { ...item, archived: true }
            : item,
        ),
      );
      return { prev, key };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(ctx.key, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["inbox", wsId] });
    },
  });
}

// Batch archive mutations — invalidate-only, matching web. The optimistic
// path isn't worth the complexity: archive-completed depends on the issue
// status of each linked issue (not carried on InboxItem), and predicting
// that on the client risks divergence with the server's SQL filter.
export function useArchiveAllInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  return useMutation({
    mutationFn: () => api.archiveAllInbox(),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["inbox", wsId] });
    },
  });
}

export function useArchiveAllReadInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  return useMutation({
    mutationFn: () => api.archiveAllReadInbox(),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["inbox", wsId] });
    },
  });
}

export function useArchiveCompletedInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  return useMutation({
    mutationFn: () => api.archiveCompletedInbox(),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["inbox", wsId] });
    },
  });
}
