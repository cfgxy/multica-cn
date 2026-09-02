import type { InboxWorkspaceUnread } from "../types";

/**
 * Pure cross-workspace unread predicates backing the workspace-switcher dot
 * (web/desktop sidebar dropdown, mobile switch-workspace sheet). Extracted
 * from queries.ts into this zero-dependency module — mirroring
 * `chat/unread.ts` — so mobile can import the SAME definitions without
 * dragging web's API client along. The platforms must agree on which
 * workspace shows a dot for the same account state ("Behavioral parity" in
 * apps/mobile/CLAUDE.md); these functions ARE the shared definition.
 *
 * Source data: `GET /api/inbox/unread-summary` (see
 * `inboxUnreadSummaryOptions`) — per-workspace unread inbox counts,
 * deduplicated Linear-style server-side (MUL-3695). Chat replies surface
 * through the same inbox notification stream, so this is the same "inbox /
 * chat threads" signal web's dot already renders.
 */

/**
 * Whether any workspace OTHER than `currentWsId` has unread inbox items.
 * Drives the aggregate switcher dot: the active workspace's own unread is
 * already surfaced by the Inbox nav count / tab badge, so it is excluded
 * here to avoid a duplicate signal.
 */
export function hasOtherWorkspaceUnread(
  summary: InboxWorkspaceUnread[],
  currentWsId: string | null | undefined,
): boolean {
  return summary.some((s) => s.workspace_id !== currentWsId && s.count > 0);
}

/**
 * Set of workspace ids that have unread inbox items. Lets the workspace
 * switcher mark WHICH workspace a pending message lives in (the aggregate
 * dot only says "somewhere else"). Workspaces with a zero count are
 * excluded.
 */
export function unreadWorkspaceIds(summary: InboxWorkspaceUnread[]): Set<string> {
  return new Set(summary.filter((s) => s.count > 0).map((s) => s.workspace_id));
}
