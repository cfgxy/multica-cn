-- RUYI-359 consolidation: absorbs 917, 919, 920, 921, 922, 937 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 916.up.sql (RUYI-359 consolidation)

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
-- The three secondary indexes on this table are inlined at the bottom of
-- this file (RUYI-359 consolidation): they build without CONCURRENTLY because
-- the table is created in this same implicit transaction (914 precedent).
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

-- >>> absorbed from 917.up.sql (RUYI-359 consolidation)

-- Run-level prompt version attribution (RUYI-183, self-evolution phase 1).
--
-- Records, at task-claim time, which prompt_version.version number of each
-- injected tier (workspace/project/squad/agent) this run actually executed
-- with. A tier that was not injected for this run is simply absent from the
-- object — never written as version 0 — so "not applicable" and "version
-- zero" stay distinguishable.
--
-- Shape: {"workspace": <int>, "project": <int>, "squad": <int>, "agent": <int>}
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS prompt_versions JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN agent_task_queue.prompt_versions IS
    'Prompt tier version numbers this run was claimed with (RUYI-183). Keys present only for tiers actually injected; absent, not zero, for tiers that were not.';

-- >>> absorbed from 919.up.sql (RUYI-359 consolidation)

-- One row per (scope, scope_id, version): the invariant every version
-- assignment relies on. Version numbers are assigned under a row lock on
-- the owning entity (workspace/project/squad/agent), so a race that still
-- got past that lock fails here instead of creating a duplicate version.
CREATE UNIQUE INDEX idx_prompt_version_scope_version
    ON prompt_version (scope, scope_id, version);

-- >>> absorbed from 920.up.sql (RUYI-359 consolidation)

-- History list and diff-by-version both read "all versions of this scope
-- entity, newest first".
CREATE INDEX idx_prompt_version_scope_created
    ON prompt_version (scope, scope_id, created_at DESC);

-- >>> absorbed from 921.up.sql (RUYI-359 consolidation)

-- Workspace-level listing/audit views filter by workspace before scope.
CREATE INDEX idx_prompt_version_workspace
    ON prompt_version (workspace_id, created_at DESC);

-- >>> absorbed from 922.up.sql (RUYI-359 consolidation)

-- One-time v1 baseline backfill (RUYI-183, self-evolution phase 1).
--
-- Every existing workspace/project/squad/agent row already carries live
-- prompt content in its business column, predating prompt_version. Without
-- a v1 row, "view history" and "diff between two versions" would have
-- nothing to show for content that already exists, and the next edit would
-- have to special-case "no prior version" instead of reverting to v1 like
-- any other version.
--
-- source='import' distinguishes this synthetic baseline from a real edit.
-- scanner_revision/gate_result stay at their table defaults ('' / '[]'):
-- this backfill is a mechanical snapshot of already-live content, not a new
-- write path, so it does not run the credential/structure gate that guards
-- actual save/switch/rollback requests.
--
-- Idempotent: a migration record that ran once and left rows behind must
-- not insert a duplicate v1 if this file is ever re-applied against a
-- database that already has them, so every INSERT is guarded by
-- WHERE NOT EXISTS on (scope, scope_id, version = 1).
INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT w.id, 'workspace', w.id, 1, COALESCE(w.context, ''), encode(digest(COALESCE(w.context, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM workspace w
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'workspace' AND pv.scope_id = w.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT p.workspace_id, 'project', p.id, 1, COALESCE(p.instructions, ''), encode(digest(COALESCE(p.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM project p
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'project' AND pv.scope_id = p.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT s.workspace_id, 'squad', s.id, 1, COALESCE(s.instructions, ''), encode(digest(COALESCE(s.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM squad s
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'squad' AND pv.scope_id = s.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT a.workspace_id, 'agent', a.id, 1, COALESCE(a.instructions, ''), encode(digest(COALESCE(a.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM agent a
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'agent' AND pv.scope_id = a.id AND pv.version = 1
);

-- >>> absorbed from 937.up.sql (RUYI-359 consolidation)

-- v1 baseline gap backfill (RUYI-213).
--
-- The v1 baseline import above was a one-time snapshot: it gave every
-- workspace/project/squad/agent that existed at the time a v1 prompt_version
-- row. Nothing wrote a baseline for entities created afterwards, so every one
-- of them has empty version history — "view history" and "diff" show nothing,
-- and the content the entity launched with is unrecoverable. The create paths
-- now seed v1 themselves; this section closes the window between that import
-- and that fix.
--
-- Idempotent by the same guard the import above used: WHERE NOT EXISTS on
-- (scope, scope_id, version = 1). An entity that already has a v1 row — from
-- 922, from the create path, or from a previous run of this file — is left
-- exactly as it is. No existing row is ever updated or deleted, so a project
-- whose instructions changed since creation keeps the older baseline it
-- already had rather than having live content written over its history.
INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT w.id, 'workspace', w.id, 1, COALESCE(w.context, ''), encode(digest(COALESCE(w.context, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM workspace w
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'workspace' AND pv.scope_id = w.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT p.workspace_id, 'project', p.id, 1, COALESCE(p.instructions, ''), encode(digest(COALESCE(p.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM project p
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'project' AND pv.scope_id = p.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT s.workspace_id, 'squad', s.id, 1, COALESCE(s.instructions, ''), encode(digest(COALESCE(s.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM squad s
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'squad' AND pv.scope_id = s.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT a.workspace_id, 'agent', a.id, 1, COALESCE(a.instructions, ''), encode(digest(COALESCE(a.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM agent a
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'agent' AND pv.scope_id = a.id AND pv.version = 1
);
