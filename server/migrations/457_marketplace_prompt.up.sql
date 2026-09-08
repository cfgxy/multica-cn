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

-- One row per draft or published snapshot. `series_id` groups the versions of
-- one asset, so there is no separate asset table: v1 and v2 of the same prompt
-- share a series_id and differ in `version`.
CREATE TABLE marketplace_prompt_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('agent_prompt', 'squad_prompt')),
    -- NULL while the row is a draft; assigned at publish time under a series
    -- lock and backed by the unique index in 458.
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
