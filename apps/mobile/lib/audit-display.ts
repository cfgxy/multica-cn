import type { AuditEvent, ListAuditEventsParams } from "@multica/core/types";

export interface AuditFilterDraft {
  since: string;
  until: string;
  timeBasis: string;
  actorID: string;
  actorType: string;
  clientName: string;
  clientVersion: string;
  toolName: string;
  resultStatus: string;
  resultErrorCode: string;
  targetType: string;
  targetID: string;
}

export function buildAuditParams(draft: AuditFilterDraft): ListAuditEventsParams {
  const value = (input: string) => input.trim();
  const params: ListAuditEventsParams = { limit: 50 };
  if (value(draft.since)) params.since = value(draft.since);
  if (value(draft.until)) params.until = value(draft.until);
  if (value(draft.actorID)) params.actor_id = value(draft.actorID);
  if (value(draft.actorType)) params.actor_type = value(draft.actorType);
  if (value(draft.clientName)) params.client_name = value(draft.clientName);
  if (value(draft.clientVersion)) params.client_version = value(draft.clientVersion);
  if (value(draft.toolName)) params.tool_name = value(draft.toolName);
  if (draft.timeBasis.trim() === "invocation_start") params.time_basis = "invocation_start";
  if (["success", "failure", "refused"].includes(draft.resultStatus)) {
    params.result_status = draft.resultStatus as ListAuditEventsParams["result_status"];
  }
  if (["invalid_input", "not_found", "permission_denied", "unavailable", "timeout", "internal"].includes(draft.resultErrorCode)) {
    params.result_error_code = draft.resultErrorCode as ListAuditEventsParams["result_error_code"];
  }
  if (["workspace", "issue", "host"].includes(draft.targetType) && value(draft.targetID)) {
    params.target_type = draft.targetType as ListAuditEventsParams["target_type"];
    params.target_id = value(draft.targetID);
  }
  return params;
}

export function nextAuditCursor(page: { next_cursor: string | null; next_cursor_id: string | null }) {
  return page.next_cursor && page.next_cursor_id
    ? { cursor: page.next_cursor, cursor_id: page.next_cursor_id }
    : undefined;
}

export function auditFailureKind(error: unknown): "permission" | "service" {
  return typeof error === "object" && error !== null && "status" in error && error.status === 403
    ? "permission"
    : "service";
}

export function safeAuditDetails(event: AuditEvent): [string, string][] {
  const allowed: [string, string][] = [
    ["Tool", "tool_name"],
    ["Result", "result_status"],
    ["Error code", "result_error_code"],
    ["Client", "reported_client_name"],
    ["Client version", "reported_client_version"],
    ["Target type", "target_type"],
  ];
  return allowed.flatMap(([label, key]) => {
    const value = event.details[key];
    return typeof value === "string" && value.length > 0 ? [[label, value]] : [];
  });
}
