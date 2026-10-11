-- Voice provider pool (RUYI-693 stage D2): the workspace-level voice
-- supplier rows the binding-free resolution chain reads. Referential
-- integrity is application-layer (house rule, no FKs): the handlers validate
-- workspace scope, and DeleteWorkspaceRuntimesAndProjects sweeps the table.

-- name: GetVoiceProvider :one
SELECT * FROM voice_provider WHERE id = $1;

-- name: ListVoiceProvidersByWorkspace :many
SELECT * FROM voice_provider WHERE workspace_id = $1 ORDER BY created_at ASC, id ASC;

-- name: CreateVoiceProvider :one
INSERT INTO voice_provider (
    workspace_id, display_name, provider_type, credential_ref, metadata,
    enabled, created_by
) VALUES (
    $1, $2, $3, sqlc.narg('credential_ref'), COALESCE(sqlc.narg('metadata'), '{}'::jsonb),
    COALESCE(sqlc.narg('enabled'), true), sqlc.narg('created_by')
)
RETURNING *;

-- name: UpdateVoiceProvider :one
UPDATE voice_provider SET
    display_name = COALESCE(sqlc.narg('display_name'), display_name),
    enabled = COALESCE(sqlc.narg('enabled'), enabled),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetVoiceProviderMetadata :exec
UPDATE voice_provider SET metadata = $2, updated_at = now() WHERE id = $1;

-- name: SetVoiceProviderCredentialRef :one
UPDATE voice_provider SET credential_ref = sqlc.narg('credential_ref'), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteVoiceProvider :exec
DELETE FROM voice_provider WHERE id = $1;

-- name: ClearAgentVoiceProvider :execrows
-- Agent override unbind (provider delete path): drops the whole triple —
-- the override is level-wise atomic, so no field survives a cleared provider.
UPDATE agent SET
    voice_provider_id = NULL,
    voice_model = NULL,
    voice_advanced = NULL,
    updated_at = now()
WHERE voice_provider_id = $1;
