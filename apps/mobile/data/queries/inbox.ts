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
