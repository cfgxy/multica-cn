-- =====================
-- Autopilot parameters (RUYI-641 stage A)
-- =====================

-- name: ListAutopilotParams :many
SELECT * FROM autopilot_param
WHERE autopilot_id = $1
ORDER BY position, created_at;

-- name: CreateAutopilotParam :one
INSERT INTO autopilot_param (
    workspace_id, autopilot_id, name, param_type, description,
    required, default_value, enum_options, position
) VALUES (
    $1, $2, $3, $4, $5,
    $6, sqlc.narg('default_value'), $7, $8
) RETURNING *;

-- name: DeleteAutopilotParams :exec
-- Replace-set semantics: the PUT /params handler deletes the whole set and
-- re-inserts the submitted definitions in one transaction.
DELETE FROM autopilot_param
WHERE autopilot_id = $1;
