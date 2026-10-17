-- RUYI-355 Phase 1: unified audit_event queries.
-- Writes go through the copyfrom batch insert below; every writer funnels
-- through server/internal/service/audit.go, which owns required-field
-- validation (never add an INSERT here from a call site).

-- name: CreateAuditEvents :copyfrom
INSERT INTO audit_event (
    id, workspace_id, domain, event_type, occurred_at,
    actor_type, actor_id, trigger_kind, trigger_ref,
    issue_id, task_id, agent_id, runtime_id,
    reason, details, invocation_id, sensitive, expires_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16, $17, $18
);

-- name: CreateMCPInvocationAuditEvent :execrows
INSERT INTO audit_event (
    id, workspace_id, domain, event_type, occurred_at,
    actor_type, actor_id, trigger_kind, trigger_ref,
    issue_id, task_id, agent_id, runtime_id,
    reason, details, invocation_id, sensitive, expires_at
) VALUES (
    sqlc.arg('id'), sqlc.arg('workspace_id'), 'mcp', sqlc.arg('event_type'), sqlc.arg('occurred_at'),
    sqlc.arg('actor_type'), sqlc.narg('actor_id'), 'mcp_invocation', sqlc.arg('invocation_id')::uuid::text,
    sqlc.narg('issue_id'), sqlc.narg('task_id'), sqlc.narg('agent_id'), sqlc.narg('runtime_id'),
    sqlc.narg('reason'), sqlc.arg('details'), sqlc.arg('invocation_id')::uuid, sqlc.arg('sensitive'), sqlc.arg('expires_at')
)
ON CONFLICT (id) DO NOTHING;

-- name: QueueMCPInvocationAuditEvent :exec
INSERT INTO mcp_invocation_audit_outbox (
    id, workspace_id, event_type, occurred_at, actor_type, actor_id,
    invocation_id, issue_id, task_id, agent_id, runtime_id, reason,
    details, sensitive, expires_at
) VALUES (
    sqlc.arg('id'), sqlc.arg('workspace_id'), sqlc.arg('event_type'), sqlc.arg('occurred_at'),
    sqlc.arg('actor_type'), sqlc.narg('actor_id'), sqlc.arg('invocation_id'),
    sqlc.narg('issue_id'), sqlc.narg('task_id'), sqlc.narg('agent_id'), sqlc.narg('runtime_id'),
    sqlc.narg('reason'), sqlc.arg('details'), sqlc.arg('sensitive'), sqlc.arg('expires_at')
)
ON CONFLICT (id) DO NOTHING;

-- name: ListMCPInvocationAuditOutbox :many
SELECT id, workspace_id, event_type, occurred_at, actor_type, actor_id,
       invocation_id, issue_id, task_id, agent_id, runtime_id, reason,
       details, sensitive, expires_at, attempts
FROM mcp_invocation_audit_outbox
WHERE expires_at > now()
  AND attempts < 10
ORDER BY created_at ASC
LIMIT sqlc.arg('limit');

-- name: MarkMCPInvocationAuditOutboxAttempt :exec
UPDATE mcp_invocation_audit_outbox
SET attempts = attempts + 1,
    last_attempt_at = now(),
    last_error_code = sqlc.arg('last_error_code')
WHERE id = sqlc.arg('id');

-- name: DeleteMCPInvocationAuditOutbox :exec
DELETE FROM mcp_invocation_audit_outbox WHERE id = sqlc.arg('id');

-- name: DeleteExpiredMCPInvocationAuditOutbox :execrows
DELETE FROM mcp_invocation_audit_outbox WHERE expires_at <= sqlc.arg('cutoff');

-- name: DeleteExpiredMCPInvocationAuditEvents :execrows
DELETE FROM audit_event
WHERE domain = 'mcp' AND expires_at IS NOT NULL AND expires_at <= sqlc.arg('cutoff');

-- name: GetMCPInvocationAuditStart :one
SELECT invocation_id, workspace_id, actor_id, sensitive, details
FROM audit_event
WHERE domain = 'mcp'
  AND event_type = 'mcp.invocation_started'
  AND invocation_id = sqlc.arg('invocation_id')
LIMIT 1;

-- name: InsertMCPPreAuthRejection :execrows
INSERT INTO mcp_pre_auth_rejection (invocation_id, instance_id, reason_code, file_tool_name, recorded_at, expires_at)
VALUES (sqlc.arg('invocation_id'), sqlc.arg('instance_id'), sqlc.arg('reason_code'), sqlc.narg('file_tool_name'), now(), now() + interval '7 days')
ON CONFLICT (invocation_id) DO NOTHING;

-- name: DeleteExpiredMCPPreAuthRejections :execrows
DELETE FROM mcp_pre_auth_rejection WHERE expires_at <= sqlc.arg('cutoff');

-- name: ListMCPPreAuthRejections :many
SELECT invocation_id, instance_id, reason_code, file_tool_name, recorded_at, expires_at
FROM mcp_pre_auth_rejection
WHERE expires_at > now()
  AND (sqlc.narg('instance_id')::text IS NULL OR instance_id = sqlc.narg('instance_id'))
  AND (sqlc.narg('reason_code')::text IS NULL OR reason_code = sqlc.narg('reason_code'))
  AND (sqlc.narg('since')::timestamptz IS NULL OR recorded_at >= sqlc.narg('since'))
  AND (sqlc.narg('until')::timestamptz IS NULL OR recorded_at <= sqlc.narg('until'))
ORDER BY recorded_at DESC, invocation_id DESC
LIMIT sqlc.arg('limit');

-- name: ListAuditEvents :many
-- Workspace-level audit search: time window + domain/event_type + actor +
-- object dimensions + reason, newest first with a (occurred_at, id) keyset
-- cursor. NULL filters are skipped (sqlc.narg keeps them nullable on the Go
-- side). The issue-level endpoint is the same query with issue_id pinned —
-- one query, one code path (RUYI-355).
SELECT * FROM audit_event
WHERE workspace_id = $1
  AND (sqlc.narg('since')::timestamptz IS NULL OR occurred_at >= sqlc.narg('since'))
  AND (sqlc.narg('until')::timestamptz IS NULL OR occurred_at <= sqlc.narg('until'))
  AND (sqlc.narg('filter_domain')::text IS NULL OR domain = sqlc.narg('filter_domain'))
  AND (sqlc.narg('filter_event_type')::text IS NULL OR event_type = sqlc.narg('filter_event_type'))
  AND (sqlc.narg('filter_actor_type')::text IS NULL OR actor_type = sqlc.narg('filter_actor_type'))
  AND (sqlc.narg('filter_actor_id')::uuid IS NULL OR actor_id = sqlc.narg('filter_actor_id'))
  AND (sqlc.narg('filter_issue_id')::uuid IS NULL OR issue_id = sqlc.narg('filter_issue_id'))
  AND (sqlc.narg('filter_task_id')::uuid IS NULL OR task_id = sqlc.narg('filter_task_id'))
  AND (sqlc.narg('filter_agent_id')::uuid IS NULL OR agent_id = sqlc.narg('filter_agent_id'))
  AND (sqlc.narg('filter_runtime_id')::uuid IS NULL OR runtime_id = sqlc.narg('filter_runtime_id'))
  AND (sqlc.narg('filter_reason')::text IS NULL OR reason = sqlc.narg('filter_reason'))
  AND (sqlc.arg('include_sensitive')::boolean OR sensitive = false)
  AND (sqlc.narg('filter_invocation_id')::uuid IS NULL OR (domain = 'mcp' AND invocation_id = sqlc.narg('filter_invocation_id')))
  AND (domain <> 'mcp' OR expires_at IS NULL OR expires_at > now())
  AND (sqlc.narg('cursor_at')::timestamptz IS NULL
       OR (occurred_at, id) < (sqlc.narg('cursor_at'), sqlc.narg('cursor_id')::uuid))
ORDER BY occurred_at DESC, id DESC
LIMIT $2;

-- name: ListAllWorkspaceIDs :many
-- Startup anchoring (RUYI-355): ops.server_started is written once per known
-- workspace at boot — every audit query is workspace-scoped, so the
-- deployment anchor must land where timeline readers will filter for it.
SELECT id FROM workspace
ORDER BY id ASC;
