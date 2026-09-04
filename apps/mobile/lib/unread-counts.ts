/**
 * Unread count hooks for the bottom tab bar badges.
 *
 * Mirrors the counting logic from:
 *   - packages/core/inbox/queries.ts::useInboxUnreadCount (inbox)
 *   - packages/core/chat/unread.ts::countUnreadChatMessages (chat — the
 *     shared pure function IS the definition; web's sidebar calls the same
 *     one, so the platforms cannot drift apart)
 *
 * Both queries (`inboxListOptions`, `chatSessionsOptions`) are already kept
 * fresh by listing-level realtime hooks mounted in
 * `app/(app)/[workspace]/_layout.tsx`, so these hooks only attach a `select`
 * to derive a scalar count — re-rendering the tab layout only when the
 * number actually changes (TQ compares select output with Object.is).
 *
 * Behavioral parity (apps/mobile/CLAUDE.md "Counts and visibility must agree"):
 * the N rendered here MUST equal the N web shows for the same user/workspace.
 */
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { countUnreadChatMessages } from "@multica/core/chat/unread";
import { unreadWorkspaceIds } from "@multica/core/inbox/unread";
import {
  inboxListOptions,
  inboxUnreadSummaryOptions,
} from "@/data/queries/inbox";
import { chatSessionsOptions } from "@/data/queries/chat";
import { deduplicateInboxItems } from "@/lib/inbox-display";

/**
 * Unread inbox count, aligned with what the inbox list renders: archived
 * items dropped, then deduplicated by issue (one entry per issue), then
 * filtered to unread. Same definition as web's sidebar badge.
 */
export function useInboxUnreadCount(wsId: string | null | undefined): number {
  const { data } = useQuery({
    ...inboxListOptions(wsId ?? null),
    select: (items) =>
      deduplicateInboxItems(items).filter((i) => !i.read).length,
  });
  return data ?? 0;
}

/**
 * Total unread assistant *messages* across chat sessions (IM-style), the
 * same number web/desktop's sidebar Chat badge shows. Was a session count
 * before MUL-4286; that matched the (since removed) web ChatFab badge and
 * disagreed with the sidebar.
 *
 * No excludeSessionId here: the chat tab renders the active conversation
 * itself, and the focused-screen auto mark-read (chat.tsx) plus the
 * mutation's optimistic unread_count reset clear that session's share of
 * the badge immediately — a badge decrementing on the tab you are already
 * inside is normal IM behavior, not a phantom.
 */
export function useChatUnreadMessageCount(
  wsId: string | null | undefined,
): number {
  const { data } = useQuery({
    ...chatSessionsOptions(wsId ?? null),
    select: (sessions) => countUnreadChatMessages(sessions),
  });
  return data ?? 0;
}

/**
 * Set of workspace ids holding unread inbox items, for the switch-workspace
 * sheet's per-workspace blue dot (RUYI-44). Consumes the same account-level
 * summary endpoint as web's sidebar and derives the set with the SAME core
 * predicate (`unreadWorkspaceIds` from @multica/core/inbox/unread — the
 * shared definition web's switcher dropdown calls), so the platforms cannot
 * disagree on which workspace shows a dot.
 *
 * `wsId` only gates fetching (the endpoint is account-level; web's call site
 * gates the same way). Memoized on the summary reference — same shape as
 * web's app-sidebar (`useMemo(() => unreadWorkspaceIds(unreadSummary),
 * [unreadSummary])`) — so consumers can call `.has()` per row without
 * re-deriving on every render.
 */
export function useWorkspaceUnreadIds(
  wsId: string | null | undefined,
): Set<string> {
  const { data: unreadSummary = [] } = useQuery(
    inboxUnreadSummaryOptions(wsId ?? null),
  );
  return useMemo(() => unreadWorkspaceIds(unreadSummary), [unreadSummary]);
}
