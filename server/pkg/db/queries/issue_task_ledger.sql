-- RUYI-971: issue structured AC and dynamic TODO execution ledger queries.

-- name: ListIssueACsByIssue :many
SELECT * FROM issue_acceptance_criteria
WHERE issue_id = $1
ORDER BY position ASC, created_at ASC;

-- name: GetIssueAC :one
SELECT * FROM issue_acceptance_criteria
WHERE id = $1 LIMIT 1;

-- name: GetIssueACByKey :one
SELECT * FROM issue_acceptance_criteria
WHERE issue_id = $1 AND key = $2 LIMIT 1;

-- name: CreateIssueAC :one
INSERT INTO issue_acceptance_criteria (
    id, workspace_id, issue_id, key, title, status, evidence, rejection_reason,
    verified_at, verified_by_type, verified_by_id, position
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
) RETURNING *;

-- name: UpdateIssueAC :one
UPDATE issue_acceptance_criteria
SET key = $2,
    title = $3,
    status = $4,
    evidence = $5,
    rejection_reason = $6,
    verified_at = $7,
    verified_by_type = $8,
    verified_by_id = $9,
    position = $10,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteIssueAC :exec
DELETE FROM issue_acceptance_criteria
WHERE id = $1;

-- name: ListIssueTodosByIssue :many
SELECT * FROM issue_todos
WHERE issue_id = $1
ORDER BY position ASC, created_at ASC;

-- name: GetIssueTodo :one
SELECT * FROM issue_todos
WHERE id = $1 LIMIT 1;

-- name: CreateIssueTodo :one
INSERT INTO issue_todos (
    id, workspace_id, issue_id, title, status, next_step, blocked_reason,
    assignee_type, assignee_id, ac_ids, evidence, position
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
) RETURNING *;

-- name: UpdateIssueTodo :one
UPDATE issue_todos
SET title = $2,
    status = $3,
    next_step = $4,
    blocked_reason = $5,
    assignee_type = $6,
    assignee_id = $7,
    ac_ids = $8,
    evidence = $9,
    position = $10,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteIssueTodo :exec
DELETE FROM issue_todos
WHERE id = $1;

-- name: CreateTaskStateAudit :one
INSERT INTO issue_task_state_audit (
    id, workspace_id, issue_id, target_type, target_id, action,
    from_status, to_status, actor_type, actor_id, evidence, note
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
) RETURNING *;

-- name: ListTaskStateAuditByIssue :many
SELECT * FROM issue_task_state_audit
WHERE issue_id = $1
ORDER BY created_at DESC;
