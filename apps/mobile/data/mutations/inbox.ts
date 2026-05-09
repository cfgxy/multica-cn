/**
 * Mobile inbox mutations. Mirrors the optimistic-update + invalidate pattern
 * of packages/core/inbox/mutations.ts useMarkInboxRead — written here in
 * mobile-owned code per Sharing Principles (no runtime imports from
 * @multica/core mutations).
 *
 * Behavioral parity:
 *   - Optimistic: flip `read` to true locally before the request lands.
 *   - On error: roll back to the previous list snapshot.
 *   - On settle: invalidate so the server's view of read-state wins.
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
