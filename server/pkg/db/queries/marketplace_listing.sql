-- Skill / MCP marketplace publishing (RUYI-99).
--
-- Every mutating statement is guarded: an update carries the revision it read,
-- a withdrawal only matches a published row, and a republication only matches a
-- withdrawn row owned by the same workspace. A guard that does not match
-- returns no row, which the handler turns into 409 / 403 rather than a silent
-- no-op.

-- name: ListPublishedMarketplaceListings :many
-- The published half of the merged catalog. Ordered to match the static
-- catalog's own kind-then-name ordering so the merge is a stable interleave.
SELECT * FROM marketplace_listing
WHERE state = 'published'
ORDER BY kind ASC, name_key ASC;

-- name: ListMarketplaceListingsBySourceWorkspace :many
-- "Published from this workspace", tombstones included: a withdrawn row still
-- shows in the management view because it is what a republication acts on.
SELECT * FROM marketplace_listing
WHERE source_workspace_id = $1
ORDER BY kind ASC, name_key ASC;

-- name: GetMarketplaceListing :one
SELECT * FROM marketplace_listing WHERE id = $1;

-- name: GetPublishedMarketplaceListing :one
-- The install path's read: a withdrawn listing must not be installable, and
-- filtering here rather than in Go keeps that rule off the caller.
SELECT * FROM marketplace_listing WHERE id = $1 AND state = 'published';

-- name: GetMarketplaceListingForUpdate :one
-- The mutation paths' read. Taking the row exclusively before checking
-- ownership and state means the check cannot go stale between the read and the
-- guarded UPDATE that follows it in the same transaction.
SELECT * FROM marketplace_listing WHERE id = $1 FOR UPDATE;

-- name: GetMarketplaceListingByNameKeyForUpdate :one
-- The publish path's conflict probe. Takes the row exclusively so two
-- concurrent publishes of the same name serialise here rather than racing to
-- the unique index; the index in 465 is still the second line of defence.
SELECT * FROM marketplace_listing
WHERE kind = $1 AND name_key = $2
FOR UPDATE;

-- name: CreateMarketplaceListing :one
INSERT INTO marketplace_listing (
    kind, name, name_key, source_workspace_id, publisher_user_id,
    publisher_display_name, summary, description, homepage_url, categories,
    source_url, config_template, placeholders,
    scanner_revision, scan_result, scanned_at, published_at
) VALUES (
    @kind, @name, @name_key, @source_workspace_id, @publisher_user_id,
    @publisher_display_name, @summary, @description, @homepage_url, @categories,
    @source_url, @config_template, @placeholders,
    @scanner_revision, @scan_result, now(), now()
)
RETURNING *;

-- name: UpdateMarketplaceListing :one
-- Optimistic update. `revision` is both the token the caller must present and
-- the value that advances, so a lost update surfaces as zero rows rather than
-- as one editor's changes vanishing. Guarded on state so a withdrawn listing
-- cannot be edited back into the catalog through this path — republication is
-- RepublishMarketplaceListing, which re-runs the scan.
UPDATE marketplace_listing SET
    name = @name,
    name_key = @name_key,
    summary = @summary,
    description = @description,
    homepage_url = @homepage_url,
    categories = @categories,
    source_url = @source_url,
    config_template = @config_template,
    placeholders = @placeholders,
    scanner_revision = @scanner_revision,
    scan_result = @scan_result,
    scanned_at = now(),
    revision = revision + 1,
    updated_at = now()
WHERE id = @id AND state = 'published' AND revision = @revision
RETURNING *;

-- name: WithdrawMarketplaceListing :one
-- Tombstone the listing. The row is kept, so (kind, name_key) stays reserved
-- (D3-A) and already-installed copies keep their provenance readable (D4-A).
-- Guarded on state so a repeat withdrawal returns no row and the handler can
-- answer 409 instead of quietly re-stamping withdrawn_at.
UPDATE marketplace_listing SET
    state = 'withdrawn',
    withdrawn_at = now(),
    withdrawn_by = @withdrawn_by,
    revision = revision + 1,
    updated_at = now()
WHERE id = @id AND state = 'published' AND revision = @revision
RETURNING *;

-- name: RepublishMarketplaceListing :one
-- Bring a tombstone back. Only reachable when the caller's workspace matches
-- source_workspace_id, which the handler checks against the locked row before
-- calling this. The scan is re-run because the content is being re-authored.
UPDATE marketplace_listing SET
    state = 'published',
    name = @name,
    summary = @summary,
    description = @description,
    homepage_url = @homepage_url,
    categories = @categories,
    source_url = @source_url,
    config_template = @config_template,
    placeholders = @placeholders,
    publisher_user_id = @publisher_user_id,
    publisher_display_name = @publisher_display_name,
    scanner_revision = @scanner_revision,
    scan_result = @scan_result,
    scanned_at = now(),
    published_at = now(),
    withdrawn_at = NULL,
    withdrawn_by = NULL,
    revision = revision + 1,
    updated_at = now()
WHERE id = @id AND state = 'withdrawn'
RETURNING *;

-- name: DeleteMarketplaceListing :execrows
-- Only used to unwind a publish whose follow-on work failed inside the same
-- request. Ordinary removal is withdrawal, which keeps the tombstone.
DELETE FROM marketplace_listing WHERE id = $1;
