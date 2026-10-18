import { queryOptions } from "@tanstack/react-query";
import type { ListAuditEventsParams } from "@multica/core/types";
import { api } from "../api";

export const auditKeys = {
  list: (workspaceId: string, params: ListAuditEventsParams) =>
    ["audit-events", workspaceId, params] as const,
};

export function auditEventsOptions(workspaceId: string | null, params: ListAuditEventsParams = {}) {
  return queryOptions({
    queryKey: auditKeys.list(workspaceId ?? "", params),
    queryFn: () => api.listAuditEvents(workspaceId ?? "", params),
    enabled: workspaceId !== null,
    staleTime: 15_000,
  });
}
