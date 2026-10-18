import { queryOptions } from "@tanstack/react-query";
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
