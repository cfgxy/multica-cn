-- RUYI-359 Phase 2R domain consolidation: merges the former migrations
-- 910, 914 into one atomic migration (renumbered to 907_marketplace) on the gap-free
-- 900+ ladder. Statement bodies are unchanged except CREATE/DROP INDEX lost
-- the CONCURRENTLY keyword: every target is created earlier in this same
-- file or by an earlier migration, and the whole file runs as one implicit
-- transaction (914 precedent); existing environments converge via the
-- ledger rewrite and never re-run these files. Original-stem -> new-stem
-- mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.

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

-- >>> from former migration 914 (RUYI-359 Phase 2R)

-- Prompt marketplace (RUYI-100): agent and squad instructions become
-- versionable, publishable marketplace assets.
--
-- Two entities carry the whole loop:
--
--   marketplace_prompt_version  one draft or one immutable published snapshot
--   workspace_prompt_install    one workspace's pointer at a version
--
-- The existing marketplace is a compile-time embedded catalog (skill/mcp) with
-- no publisher, no version and no immutable content hash, so it cannot express
-- publish / withdraw / install provenance / restore. Rather than widen
-- MarketplaceItem into a generic JSONB payload shared with RUYI-99's skill and
-- MCP publishing, prompts get their own typed pair; the shared publish state
-- machine lives in Go, not in one overloaded table.
--
-- No foreign keys by house rule: workspace_id / source_id / publisher_user_id /
-- installed_version_id integrity is enforced in the application layer, and every
-- write path re-validates its references inside the transaction that uses them.
--
-- Tables, constraints and indexes all live in this one migration. The indexes
-- are built non-concurrently on purpose: every one of them covers a table this
-- same file creates, so at build time the relation is empty and unreachable by
-- any other session. CONCURRENTLY would buy nothing there, and it cannot be
-- used here anyway — the runner executes a migration file as one multi-command
-- string, which PostgreSQL wraps in an implicit transaction. The
-- one-statement-per-file rule applies to indexes added to tables that already
-- carry live traffic.

-- One row per draft or published snapshot. `series_id` groups the versions of
-- one asset, so there is no separate asset table: v1 and v2 of the same prompt
-- share a series_id and differ in `version`.
CREATE TABLE marketplace_prompt_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('agent_prompt', 'squad_prompt')),
    -- NULL while the row is a draft; assigned at publish time under a series
    -- lock and backed by idx_marketplace_prompt_version_series_version below.
    version INT CHECK (version IS NULL OR version > 0),

    -- Publication provenance, snapshotted at publish time. source_workspace_id
    -- is stored for withdrawal authority only and must never appear in any
    -- response: exposing it across workspaces would leak org structure (D4).
    source_workspace_id UUID NOT NULL,
    source_type TEXT NOT NULL CHECK (source_type IN ('agent', 'squad')),
    source_id UUID NOT NULL,
    publisher_user_id UUID NOT NULL,
    publisher_display_name TEXT NOT NULL DEFAULT '',

    -- The snapshot itself. `content` is the source object's persisted user
    -- instructions at publish time; for Mika that is agent.instructions only,
    -- never the binary-embedded system segment.
    content TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '' CHECK (char_length(name) <= 100),
    summary TEXT NOT NULL DEFAULT '' CHECK (char_length(summary) <= 200),
    audience TEXT NOT NULL DEFAULT '' CHECK (char_length(audience) <= 200),
    categories JSONB NOT NULL DEFAULT '[]'::jsonb,
    license_code TEXT NOT NULL DEFAULT '',
    usage_notes TEXT NOT NULL DEFAULT '' CHECK (char_length(usage_notes) <= 500),
    companions TEXT NOT NULL DEFAULT '' CHECK (char_length(companions) <= 500),

    state TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'published', 'withdrawn')),
    visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public')),

    -- Which detector revision cleared this snapshot. A pass means "no rule of
    -- this revision matched", never "contains no secret", so the revision has
    -- to travel with the row for any future re-scan to be meaningful.
    scanner_revision TEXT NOT NULL DEFAULT '',
    -- Findings as category / rule / line only. The matched text never lands
    -- here, in a log, or in an error body.
    scan_result JSONB NOT NULL DEFAULT '[]'::jsonb,
    scanned_at TIMESTAMPTZ,

    idempotency_key TEXT,
    published_at TIMESTAMPTZ,
    withdrawn_at TIMESTAMPTZ,
    withdrawn_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- A published row is a frozen snapshot: it must carry its version, its
    -- content hash and the scan that cleared it. Enforced here as well as in
    -- the handler because publishing assigns several columns at once and a
    -- half-assigned publish would be discoverable.
    CONSTRAINT marketplace_prompt_version_published_complete CHECK (
        state <> 'published'
        OR (version IS NOT NULL AND content_sha256 <> '' AND scanned_at IS NOT NULL)
    )
);

COMMENT ON TABLE marketplace_prompt_version IS
    'One draft or immutable published prompt snapshot (RUYI-100). Published rows are never updated in place; a new version is a new row sharing series_id. source_workspace_id is withdrawal authority only and must not be returned by any API.';

-- One row per (workspace, series): what this workspace installed and at which
-- version. Installing does NOT touch any agent or squad — applying does, and
-- that is a separate explicit act (Owner baseline 7).
CREATE TABLE workspace_prompt_install (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    series_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('agent_prompt', 'squad_prompt')),
    installed_version_id UUID NOT NULL,
    installed_version INT NOT NULL,
    installed_content_sha256 TEXT NOT NULL DEFAULT '',

    -- Display snapshot so the installed list never has to read back across a
    -- workspace boundary, and keeps rendering after the source is withdrawn.
    name TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    publisher_display_name TEXT NOT NULL DEFAULT '',
    license_code TEXT NOT NULL DEFAULT '',

    installed_by UUID,
    installed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE workspace_prompt_install IS
    'One workspace''s pointer at a prompt series version (RUYI-100). Installing creates the row and changes no agent or squad; applying writes the content into a target and is a separate transaction.';

-- Where the last apply came from, and the one text it replaced. Not a new
-- entity and never returned by the ordinary agent/squad responses: it is the
-- transactional companion of `instructions`, written in the same statement pair
-- so a failed apply leaves both at their previous values.
--
-- Shape: {"install_id","version_id","series_id","applied_version",
--         "applied_content_sha256","previous_text","previous_updated_at",
--         "applied_by","applied_at","last_operation_id"}
-- Only ONE previous text is kept: this feature promises one-step restore, not
-- version history.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS marketplace_prompt_state JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE squad ADD COLUMN IF NOT EXISTS marketplace_prompt_state JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN agent.marketplace_prompt_state IS
    'Last marketplace prompt apply on this agent plus the single text it replaced (RUYI-100). Internal: never included in an agent API response.';
COMMENT ON COLUMN squad.marketplace_prompt_state IS
    'Last marketplace prompt apply on this squad plus the single text it replaced (RUYI-100). Internal: never included in a squad API response.';

-- A series has at most one row per version number. This is the guard that makes
-- concurrent publishes safe: version assignment takes a series-level lock, and
-- if two publishes still raced past it, the second one fails here instead of
-- creating a duplicate v2.
CREATE UNIQUE INDEX idx_marketplace_prompt_version_series_version
    ON marketplace_prompt_version (series_id, version)
    WHERE version IS NOT NULL;

-- One open draft per series. Publishing a v2 starts from a fresh draft, and two
-- half-filled drafts for the same asset would make "continue the draft" in the
-- source object's status bar ambiguous.
CREATE UNIQUE INDEX idx_marketplace_prompt_version_series_draft
    ON marketplace_prompt_version (series_id)
    WHERE state = 'draft';

-- Idempotency-Key is scoped to its publisher: the same key from two users is
-- two distinct requests, and a global key space would let one user's retry
-- collide with another's first attempt.
CREATE UNIQUE INDEX idx_marketplace_prompt_version_idempotency
    ON marketplace_prompt_version (publisher_user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Discovery lists published public versions newest first. Partial on the two
-- state columns because everything else — drafts, withdrawn rows, private
-- drafts — is only ever read by id or by source object.
CREATE INDEX idx_marketplace_prompt_version_discovery
    ON marketplace_prompt_version (published_at DESC, id)
    WHERE state = 'published' AND visibility = 'public';

-- The status bar on an agent/squad prompt tab asks "what did this object
-- publish, and is there an open draft" on every render, which is a lookup by
-- source object rather than by series.
CREATE INDEX idx_marketplace_prompt_version_source
    ON marketplace_prompt_version (source_workspace_id, source_type, source_id);

-- One install row per (workspace, series). Reinstalling the same version is
-- idempotent and updating to a newer version moves this row's pointer, so a
-- second row for the same asset would fragment "installed" and "update
-- available" state.
CREATE UNIQUE INDEX idx_workspace_prompt_install_workspace_series
    ON workspace_prompt_install (workspace_id, series_id);
