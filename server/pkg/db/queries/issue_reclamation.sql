-- name: ReserveIssueReclamation :one
SELECT reserve_issue_reclamation(
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('issue_id')::uuid,
    sqlc.arg('daemon_id')::text,
    sqlc.arg('owner_id')::uuid,
    sqlc.arg('token_hash')::bytea,
    sqlc.arg('finish')::boolean
)::text;
