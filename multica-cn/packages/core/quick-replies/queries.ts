import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { QuickReply } from "../types";

/**
 * The workspace quick-reply catalog (RUYI-435).
 *
 * One ordered list per workspace. Read by every member's composer menu;
 * written only by owner/admin through the settings tab or MCP. The list is
 * the single source both management surfaces and all three composers render.
 */

export const quickReplyKeys = {
  all: (wsId: string) => ["quick-replies", wsId] as const,
  list: (wsId: string) => [...quickReplyKeys.all(wsId), "list"] as const,
};

export function quickReplyListOptions(wsId: string) {
  return queryOptions({
    queryKey: quickReplyKeys.list(wsId),
    queryFn: () => api.listQuickReplies(),
    select: (data) => data.quick_replies,
    // The catalog changes only when an admin edits it, which is rare — but a
    // composer menu that opens after an MCP edit must show the new row, so
    // unlike the status catalog this keeps the default freshness (refetch on
    // mount) and leans on the quick_reply:changed realtime event for live
    // convergence instead of a long staleTime.
    staleTime: 30_000,
  });
}

/** Sorts a quick-reply list into display order (defensive; server pre-sorts). */
export function compareQuickReplies(a: QuickReply, b: QuickReply): number {
  if (a.position !== b.position) return a.position - b.position;
  return a.created_at.localeCompare(b.created_at);
}
