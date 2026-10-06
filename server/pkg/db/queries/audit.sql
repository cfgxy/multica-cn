-- RUYI-355 Phase 1: unified audit_event queries.
-- Writes go through the copyfrom batch insert below; every writer funnels
-- through server/internal/service/audit.go, which owns required-field
-- validation (never add an INSERT here from a call site).

-- name: CreateAuditEvents :copyfrom
INSERT INTO audit_event (
    id, workspace_id, domain, event_type, occurred_at,
    actor_type, actor_id, trigger_kind, trigger_ref,
    issue_id, task_id, agent_id, runtime_id,
    reason, details
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15
);

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
