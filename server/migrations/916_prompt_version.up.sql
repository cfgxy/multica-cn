-- Prompt version history (RUYI-183, self-evolution phase 1): version
-- snapshot management for the four prompt tiers — workspace context,
-- project instructions, squad instructions, agent instructions.
--
-- The four tiers keep their existing plain-TEXT business columns
-- (workspace.context, project.instructions, squad.instructions,
-- agent.instructions) as the single read source for "currently effective
-- content". This table is history and audit only: the injection hot path
-- (daemon task claim, execenv runtime sections, squad briefing, project
-- resource) must keep reading the business column directly and never join
-- against this table.
--
-- Every effective-content change — edit-save, switch to a historical
-- version, or rollback — is the same underlying write: append a new row
-- here and copy its content into the business column in one transaction.
-- Switching/rolling back never rewrites or deletes an existing row, so the
-- version line itself is the audit trail; there is no separate event-log
-- table (see RUYI-179 ADR-001 §4.2).
--
-- No foreign keys by house rule: workspace_id / scope_id / author_user_id
-- integrity is enforced in the application layer, and every write path
-- re-validates its references inside the transaction that uses them.
--
-- Indexes are not created here: every CREATE INDEX must use CONCURRENTLY,
-- which cannot run inside this multi-statement file or a transaction —
-- see 919/920/921 for the three indexes on this table.
CREATE TABLE prompt_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'project', 'squad', 'agent')),
    scope_id UUID NOT NULL,
    version INT NOT NULL CHECK (version > 0),
    content TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL,

    -- Provenance of this write. 'import' is the one-time v1 baseline
    -- created from pre-existing content; 'edit' is an explicit save;
    -- 'revert' is a copy-forward rollback (source_version records which
    -- version's content was copied); 'auto_snapshot' is reserved for a
    -- future automatic checkpoint, not written by this issue's code.
    source TEXT NOT NULL CHECK (source IN ('import', 'edit', 'revert', 'auto_snapshot')),
    source_version INT,
    change_note TEXT NOT NULL DEFAULT '',

    -- Which scanner revision cleared this content, and what it found. A
    -- pass means "no rule of this revision matched", never "contains no
    -- secret", so the revision travels with the row. Findings are
    -- category / rule / line only — matched text never lands here, in a
    -- log, or in an error body.
    scanner_revision TEXT NOT NULL DEFAULT '',
    gate_result JSONB NOT NULL DEFAULT '[]'::jsonb,

    author_user_id UUID,
    author_note_issue_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE prompt_version IS
    'Version history for the four prompt tiers (RUYI-183). History/audit only — the business column on workspace/project/squad/agent stays the single read source for currently effective content. Append-only: switching or rolling back writes a new row, never mutates or deletes an existing one.';
