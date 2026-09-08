-- Prompt marketplace (RUYI-100).
--
-- Published rows are never updated in place: UpdatePromptVersionDraft is
-- guarded on state = 'draft', and PublishPromptVersion is the only statement
-- that sets state = 'published'. There is deliberately no "update published
-- content" query — a new version is a new row.

-- name: CreatePromptVersionDraft :one
INSERT INTO marketplace_prompt_version (
    series_id, kind, source_workspace_id, source_type, source_id,
    publisher_user_id, publisher_display_name,
    content, content_sha256, name, summary, audience, categories,
    license_code, usage_notes, companions, idempotency_key
) VALUES (
    @series_id, @kind, @source_workspace_id, @source_type, @source_id,
    @publisher_user_id, @publisher_display_name,
    @content, @content_sha256, @name, @summary, @audience, @categories,
    @license_code, @usage_notes, @companions, sqlc.narg('idempotency_key')
)
RETURNING *;

-- name: GetPromptVersion :one
SELECT * FROM marketplace_prompt_version WHERE id = $1;

-- name: GetPromptVersionForUpdate :one
SELECT * FROM marketplace_prompt_version WHERE id = $1 FOR UPDATE;

-- name: GetPromptVersionByIdempotencyKey :one
SELECT * FROM marketplace_prompt_version
WHERE publisher_user_id = $1 AND idempotency_key = $2;

-- name: UpdatePromptVersionDraft :one
-- Metadata edits, and optionally a refreshed content snapshot pulled again from
-- the source object. The state guard is what keeps a published snapshot
-- immutable: this returns no row rather than mutating a frozen version.
UPDATE marketplace_prompt_version SET
    name = COALESCE(sqlc.narg('name'), name),
    summary = COALESCE(sqlc.narg('summary'), summary),
    audience = COALESCE(sqlc.narg('audience'), audience),
    categories = COALESCE(sqlc.narg('categories'), categories),
    license_code = COALESCE(sqlc.narg('license_code'), license_code),
    usage_notes = COALESCE(sqlc.narg('usage_notes'), usage_notes),
    companions = COALESCE(sqlc.narg('companions'), companions),
    content = COALESCE(sqlc.narg('content'), content),
    content_sha256 = COALESCE(sqlc.narg('content_sha256'), content_sha256),
    updated_at = now()
WHERE id = @id AND state = 'draft'
RETURNING *;

-- name: RecordPromptVersionScan :one
UPDATE marketplace_prompt_version SET
    scanner_revision = @scanner_revision,
    scan_result = @scan_result,
    scanned_at = now(),
    updated_at = now()
WHERE id = @id AND state = 'draft'
RETURNING *;

-- name: NextPromptSeriesVersion :one
-- Next version number in a series. Called while the series' published rows are
-- locked by LockPromptSeries, so the read cannot slide under a concurrent
-- publish; the unique index on (series_id, version) is the second line.
SELECT COALESCE(MAX(version), 0)::int + 1 AS next_version
FROM marketplace_prompt_version
WHERE series_id = $1;

-- name: LockPromptSeries :many
-- Series-level lock for version assignment. Locks every row of the series so a
-- concurrent publish of the same asset serialises behind this one.
SELECT id FROM marketplace_prompt_version WHERE series_id = $1 FOR UPDATE;

-- name: PublishPromptVersion :one
UPDATE marketplace_prompt_version SET
    state = 'published',
    visibility = @visibility,
    version = @version,
    published_at = now(),
    updated_at = now()
WHERE id = @id AND state = 'draft'
RETURNING *;

-- name: WithdrawPromptVersion :one
UPDATE marketplace_prompt_version SET
    state = 'withdrawn',
    withdrawn_at = now(),
    withdrawn_by = @withdrawn_by,
    updated_at = now()
WHERE id = @id AND state = 'published'
RETURNING *;

-- name: ListPublishedPromptVersions :many
-- Discovery. Only the latest published version of each series is listed, so a
-- series with v1 and v2 shows once; withdrawn versions drop their whole series
-- out of discovery only when no published version remains.
SELECT DISTINCT ON (series_id) *
FROM marketplace_prompt_version
WHERE state = 'published'
  AND visibility = 'public'
  AND (sqlc.narg('kind')::text IS NULL OR kind = sqlc.narg('kind')::text)
  AND (
      sqlc.narg('query')::text IS NULL
      OR name ILIKE '%' || sqlc.narg('query')::text || '%'
      OR summary ILIKE '%' || sqlc.narg('query')::text || '%'
      OR audience ILIKE '%' || sqlc.narg('query')::text || '%'
      OR publisher_display_name ILIKE '%' || sqlc.narg('query')::text || '%'
  )
ORDER BY series_id, version DESC;

-- name: ListPromptVersionsBySeries :many
SELECT * FROM marketplace_prompt_version
WHERE series_id = $1
ORDER BY version DESC NULLS FIRST, created_at DESC;

-- name: ListPromptVersionsBySource :many
-- Everything this source object has published or has open as a draft. Drives
-- the status bar on the agent/squad prompt tab.
SELECT * FROM marketplace_prompt_version
WHERE source_workspace_id = $1 AND source_type = $2 AND source_id = $3
ORDER BY created_at DESC;

-- name: UpsertPromptInstall :one
-- Install, or move an existing install's pointer to another version. Version
-- and hash are overwritten wholesale: an install always names exactly one
-- version, and re-installing the same version is a no-op update rather than a
-- second row.
INSERT INTO workspace_prompt_install (
    workspace_id, series_id, kind, installed_version_id, installed_version,
    installed_content_sha256, name, summary, publisher_display_name,
    license_code, installed_by
) VALUES (
    @workspace_id, @series_id, @kind, @installed_version_id, @installed_version,
    @installed_content_sha256, @name, @summary, @publisher_display_name,
    @license_code, @installed_by
)
ON CONFLICT (workspace_id, series_id) DO UPDATE SET
    installed_version_id = EXCLUDED.installed_version_id,
    installed_version = EXCLUDED.installed_version,
    installed_content_sha256 = EXCLUDED.installed_content_sha256,
    name = EXCLUDED.name,
    summary = EXCLUDED.summary,
    publisher_display_name = EXCLUDED.publisher_display_name,
    license_code = EXCLUDED.license_code,
    updated_at = now()
RETURNING *;

-- name: GetPromptInstall :one
SELECT * FROM workspace_prompt_install WHERE id = $1 AND workspace_id = $2;

-- name: GetPromptInstallForUpdate :one
SELECT * FROM workspace_prompt_install WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: GetPromptInstallBySeries :one
SELECT * FROM workspace_prompt_install WHERE workspace_id = $1 AND series_id = $2;

-- name: ListPromptInstalls :many
SELECT * FROM workspace_prompt_install
WHERE workspace_id = $1
ORDER BY installed_at DESC;

-- name: DeletePromptInstallsByWorkspace :exec
-- Workspace teardown. No FK by house rule, so the delete path sweeps this
-- table explicitly inside the same transaction as the workspace delete.
DELETE FROM workspace_prompt_install WHERE workspace_id = $1;

-- name: GetAgentPromptStateForUpdate :one
SELECT id, workspace_id, owner_id, instructions, marketplace_prompt_state, updated_at
FROM agent WHERE id = $1 FOR UPDATE;

-- name: GetSquadPromptStateForUpdate :one
SELECT id, workspace_id, creator_id, instructions, marketplace_prompt_state, updated_at
FROM squad WHERE id = $1 FOR UPDATE;

-- name: ApplyPromptToAgent :one
-- The whole point of the pair: instructions and the restore state move in one
-- statement, so a failed apply can never leave the target holding new content
-- with a stale restore pointer (or the reverse).
UPDATE agent SET
    instructions = @instructions,
    marketplace_prompt_state = @marketplace_prompt_state,
    updated_at = now()
WHERE id = @id
RETURNING id, instructions, marketplace_prompt_state, updated_at;

-- name: ApplyPromptToSquad :one
UPDATE squad SET
    instructions = @instructions,
    marketplace_prompt_state = @marketplace_prompt_state,
    updated_at = now()
WHERE id = @id
RETURNING id, instructions, marketplace_prompt_state, updated_at;
