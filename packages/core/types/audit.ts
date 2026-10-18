export interface AuditEvent {
  id: string;
  workspace_id: string;
  domain: string;
  event_type: string;
  occurred_at: string | null;
  actor_type: string;
  actor_id: string | null;
  trigger_kind: string | null;
  trigger_ref: string | null;
  issue_id: string | null;
  task_id: string | null;
  agent_id: string | null;
  runtime_id: string | null;
  reason: string | null;
  invocation_id?: string;
  sensitive: boolean;
  details: Record<string, unknown>;
}

export interface AuditEventsPage {
  events: AuditEvent[];
  next_cursor: string | null;
  next_cursor_id: string | null;
}

export interface ListAuditEventsParams {
  since?: string;
  until?: string;
  time_basis?: "invocation_start";
  actor_id?: string;
  actor_type?: string;
  client_name?: string;
  client_version?: string;
  tool_name?: string;
  result_status?: "success" | "failure" | "refused";
  result_error_code?: "invalid_input" | "not_found" | "permission_denied" | "unavailable" | "timeout" | "internal";
  target_type?: "workspace" | "issue" | "host";
  target_id?: string;
  limit?: number;
  cursor?: string;
  cursor_id?: string;
}
