import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

/**
 * Inbox cache key factory.
 *
 * Shape mirrors web's `packages/core/inbox/queries.ts` — `["inbox", wsId, "list"]`
 * — so cross-platform mental model stays the same. Keying on wsId means
 * workspace switches naturally invalidate (TQ sees a new key and refetches).
 */
export const inboxKeys = {
  all: (wsId: string | null) => ["inbox", wsId] as const,
  list: (wsId: string | null) =>
    [...inboxKeys.all(wsId), "list"] as const,
  // Archived sub-view cache (RUYI-532), same shape as web's
  // packages/core/inbox/queries.ts — the family key `all` above invalidates
  // both lists together, exactly like the web implementation.
  archived: (wsId: string | null) =>
    [...inboxKeys.all(wsId), "archived"] as const,
  // Account-level (NOT workspace-scoped): one shared cache entry holding
  // unread counts for every workspace the user belongs to. Same key shape as
  // web's packages/core/inbox/queries.ts, so the mental model stays aligned.
  unreadSummary: () => ["inbox", "unread-summary"] as const,
};

export const inboxListOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: inboxKeys.list(wsId),
    queryFn: ({ signal }) => api.listInbox({ signal }),
    enabled: !!wsId,
  });

/**
 * Archived notifications, backing the inbox's "Archived" sub-view. A separate
 * cache entry from the main list (mirrors web's archivedInboxListOptions):
 * the archive grows without end, so it is fetched from its own capped
 * endpoint, and the server — not the client — decides which issues belong in
 * which list. Also fetched while showing the MAIN list: the archive entry's
 * count label needs it before the user ever goes there.
 */
export const archivedInboxOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: inboxKeys.archived(wsId),
    queryFn: ({ signal }) => api.listArchivedInbox({ signal }),
    enabled: !!wsId,
  });

/**
 * Cross-workspace unread inbox summary (GET /api/inbox/unread-summary).
 * Backs the switch-workspace sheet's per-workspace blue dot (RUYI-44) —
 * the same endpoint and derived predicates web's sidebar switcher uses
 * (`unreadWorkspaceIds` from @multica/core/inbox/unread), so the platforms
 * cannot disagree on which workspace carries unread.
 *
 * Gated on an active workspace like web's call site: the sheet only renders
 * inside a workspace route, and the shared account-level cache means the
 * query neither refetches nor goes stale across workspace switches.
 */
export const inboxUnreadSummaryOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: inboxKeys.unreadSummary(),
    queryFn: ({ signal }) => api.getInboxUnreadSummary({ signal }),
    enabled: !!wsId,
  });
