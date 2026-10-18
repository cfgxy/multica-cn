import { queryOptions } from "@tanstack/react-query";
import type { ListAuditEventsParams } from "@multica/core/types";
import { api } from "../api";

export const auditKeys = {
  all: (workspaceId: string) => ["audit-events", workspaceId] as const,
  list: (workspaceId: string, params: ListAuditEventsParams) =>
    [...auditKeys.all(workspaceId), params] as const,
};

export function auditEventsOptions(workspaceId: string | null, params: ListAuditEventsParams = {}) {
  return queryOptions({
    queryKey: auditKeys.list(workspaceId ?? "", params),
    queryFn: ({ signal }) => api.listAuditEvents(workspaceId ?? "", params, { signal }),
    enabled: workspaceId !== null,
    staleTime: 15_000,
  });
}
