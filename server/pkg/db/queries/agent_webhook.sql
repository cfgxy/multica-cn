-- name: CreateAgentWebhook :one
INSERT INTO agent_webhook (id, workspace_id, agent_id, name, prompt, token, enabled, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListAgentWebhooksByAgent :many
SELECT * FROM agent_webhook
WHERE agent_id = $1
ORDER BY created_at DESC, id DESC;

-- name: CountAgentWebhooksByAgent :one
SELECT count(*)::int FROM agent_webhook WHERE agent_id = $1;

-- name: GetAgentWebhook :one
-- Workspace-scoped so an id guessed from another workspace cannot be read.
SELECT * FROM agent_webhook
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateAgentWebhook :one
UPDATE agent_webhook
SET name = $2, prompt = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAgentWebhookEnabled :one
UPDATE agent_webhook
SET enabled = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RotateAgentWebhookToken :one
UPDATE agent_webhook
SET token = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteAgentWebhook :exec
DELETE FROM agent_webhook WHERE id = $1;

-- name: GetAgentWebhookByToken :one
-- Public ingress lookup: the token IS the credential, so this query is
-- deliberately NOT workspace-scoped — the row carries its own workspace.
SELECT * FROM agent_webhook WHERE token = $1;
