/**
 * Workspace quick-reply catalog (RUYI-435).
 *
 * Key shape mirrors web's `packages/core/quick-replies/queries.ts` —
 * `["quick-replies", wsId, "list"]` — so the cross-platform mental model
 * stays the same. Keying on wsId means a workspace switch naturally refetches.
 *
 * Mobile is a consumer only: admins manage the catalog on web's settings
 * screen (or via MCP). The 30s staleTime matches web, and the realtime
 * `quick_reply:changed` event invalidates this key the same way.
 */
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const quickReplyKeys = {
  all: (wsId: string | null) => ["quick-replies", wsId] as const,
  list: (wsId: string | null) => [...quickReplyKeys.all(wsId), "list"] as const,
};

export const quickReplyListOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: quickReplyKeys.list(wsId),
    queryFn: async ({ signal }) => {
      const res = await api.listQuickReplies({ signal });
      return res.quick_replies;
    },
    enabled: !!wsId,
    staleTime: 30_000,
  });
