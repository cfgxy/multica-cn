import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { ListAuditEventsParams } from "../types/audit";

export const auditKeys = {
  all: (workspaceId: string) => ["audit-events", workspaceId] as const,
  list: (workspaceId: string, params: ListAuditEventsParams) =>
    [...auditKeys.all(workspaceId), params] as const,
};

export function auditEventsOptions(workspaceId: string, params: ListAuditEventsParams = {}) {
  return queryOptions({
    queryKey: auditKeys.list(workspaceId, params),
    queryFn: () => api.listAuditEvents(workspaceId, params),
    staleTime: 15_000,
  });
}

/** Audit cursors are a timestamp and UUID pair. Keep them together so a page
 * boundary cannot silently duplicate or skip events that share a timestamp. */
export function auditEventsPageOptions(
  workspaceId: string,
  filters: ListAuditEventsParams = {},
) {
  const { cursor: _cursor, cursor_id: _cursorId, ...baseFilters } = filters;
  return infiniteQueryOptions({
    queryKey: auditKeys.list(workspaceId, baseFilters),
    initialPageParam: null as { cursor: string; cursor_id: string } | null,
    queryFn: ({ pageParam }) =>
      api.listAuditEvents(workspaceId, {
        ...baseFilters,
        ...(pageParam ?? {}),
      }),
    getNextPageParam: (lastPage) =>
      lastPage.next_cursor && lastPage.next_cursor_id
        ? { cursor: lastPage.next_cursor, cursor_id: lastPage.next_cursor_id }
        : undefined,
    enabled: !!workspaceId,
    staleTime: 15_000,
  });
}
