-- RUYI-359 consolidation: absorbs 911, 912, 913 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 910.up.sql (RUYI-359 consolidation)

-- Skill / MCP marketplace publishing (RUYI-99): the curated compile-time
-- catalog gains a workspace-published half.
--
-- The existing marketplace is `server/internal/service/marketplace_catalog/*.json`
-- embedded at build time: no publisher, no revision, no withdrawal. Publishing
-- needs all three, so D2-A adds this table and the listing endpoint merges the
-- two halves in Go rather than rewriting the static catalog into a table.
--
-- One row is one published (or withdrawn) listing. There is no version history
-- table: D-scope excludes semver and historical versions, so an update rewrites
-- the row in place and bumps `revision`, which is also the optimistic
-- concurrency token.
--
-- No foreign keys by house rule: source_workspace_id / publisher_user_id /
-- withdrawn_by integrity is enforced in the application layer, and every write
-- path re-validates its references inside the transaction that uses them.
CREATE TABLE marketplace_listing (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind TEXT NOT NULL CHECK (kind IN ('skill', 'mcp')),

    -- `name` is what an install creates the skill / MCP server under, so it
    -- follows the same character rule those two write paths enforce.
    -- `name_key` is its lowercase normalisation and carries the D3-A global
    -- uniqueness index: two publishers must not be able to ship "Filesystem"
    -- and "filesystem" as different things.
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    name_key TEXT NOT NULL CHECK (char_length(name_key) BETWEEN 1 AND 100),

    -- Publication provenance. source_workspace_id is withdrawal and
    -- re-publication authority (D3-A) and must never appear in a cross-workspace
    -- response: exposing it would leak org structure. publisher_display_name is
    -- the attribution the catalog actually shows.
    source_workspace_id UUID NOT NULL,
    publisher_user_id UUID NOT NULL,
    publisher_display_name TEXT NOT NULL DEFAULT '',

    -- Discovery metadata, mirroring the static catalog's MarketplaceItem so the
    -- merged listing has one shape.
    summary TEXT NOT NULL DEFAULT '' CHECK (char_length(summary) <= 200),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    homepage_url TEXT NOT NULL DEFAULT '',
    categories JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Skill listings carry a public source_url the install hands to the
    -- existing skill import path. Nothing about the publisher's own installed
    -- copy is read: publishing a skill republishes a public address, not a
    -- workspace's stored bytes.
    source_url TEXT NOT NULL DEFAULT '',

    -- MCP listings carry a TEMPLATE plus the placeholders an installer must
    -- fill. The template is authored in the publish wizard, never read back out
    -- of workspace_mcp_server.config — that column is write-only and stays so.
    -- Any credential-shaped field must be a registered `${placeholder}`, which
    -- the handler enforces before this row is written.
    config_template JSONB NOT NULL DEFAULT '{}'::jsonb,
    placeholders JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- 'published' is discoverable and installable. 'withdrawn' is a tombstone:
    -- D3-A keeps the row so the name stays reserved, and only the original
    -- source workspace may bring it back.
    state TEXT NOT NULL DEFAULT 'published' CHECK (state IN ('published', 'withdrawn')),

    -- Optimistic concurrency token. An update sends the revision it read; the
    -- statement is guarded on it, so a second editor's write fails with a
    -- conflict instead of silently overwriting the first.
    revision INT NOT NULL DEFAULT 1 CHECK (revision > 0),

    -- Which detector revision cleared this listing. A pass means "no rule of
    -- this revision matched", never "contains no secret", so the revision has
    -- to travel with the row for any future re-scan to be meaningful.
    scanner_revision TEXT NOT NULL DEFAULT '',
    -- Findings as category / rule / field / line only. The matched text never
    -- lands here, in a log, or in an error body.
    scan_result JSONB NOT NULL DEFAULT '[]'::jsonb,
    scanned_at TIMESTAMPTZ,

    published_at TIMESTAMPTZ,
    withdrawn_at TIMESTAMPTZ,
    withdrawn_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- A published row must be installable: a skill needs its source, an MCP
    -- entry needs its template, and both must carry the scan that cleared them.
    -- Checked here as well as in the handler because publishing assigns several
    -- columns at once and a half-assigned publish would be discoverable.
    CONSTRAINT marketplace_listing_published_complete CHECK (
        state <> 'published'
        OR (
            scanned_at IS NOT NULL
            AND published_at IS NOT NULL
            AND (kind <> 'skill' OR source_url <> '')
            AND (kind <> 'mcp' OR config_template <> '{}'::jsonb)
        )
    )
);

COMMENT ON TABLE marketplace_listing IS
    'One workspace-published skill or MCP marketplace listing (RUYI-99). Merged with the embedded static catalog at read time. A withdrawn row is a tombstone that keeps its (kind, name_key) reserved; only source_workspace_id may republish it. source_workspace_id is authority only and must not be returned by any API.';

COMMENT ON COLUMN marketplace_listing.config_template IS
    'MCP entry template authored in the publish wizard. Credential-bearing fields must be registered ${placeholder} tokens; the local workspace_mcp_server.config is never read to build this.';

-- >>> absorbed from 911.up.sql (RUYI-359 consolidation)

-- D3-A: `kind` + lowercase-normalised name is globally unique across every
-- workspace, and the uniqueness survives withdrawal — a tombstone keeps its
-- name reserved so a withdrawn listing cannot be impersonated by a different
-- publisher.
--
-- Inlined by RUYI-359 (marketplace_listing is created above in this same
-- implicit transaction, so the build does not need CONCURRENTLY).
CREATE UNIQUE INDEX IF NOT EXISTS idx_marketplace_listing_kind_name_key
    ON marketplace_listing (kind, name_key);

-- >>> absorbed from 912.up.sql (RUYI-359 consolidation)

-- Discovery lists published rows by kind then name, matching the static
-- catalog's own ordering so the merged listing is stable. Partial on state
-- because withdrawn tombstones are only ever read by (kind, name_key) or by
-- source workspace.
CREATE INDEX IF NOT EXISTS idx_marketplace_listing_discovery
    ON marketplace_listing (kind, name_key)
    WHERE state = 'published';

-- >>> absorbed from 913.up.sql (RUYI-359 consolidation)

-- "What has this workspace published" is its own management view, and the
-- withdrawal / republication authority check reads by source workspace on every
-- publish, update and withdraw.
CREATE INDEX IF NOT EXISTS idx_marketplace_listing_source_workspace
    ON marketplace_listing (source_workspace_id, kind, name_key);
