-- RUYI-359 consolidation: absorbs 971 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 970.up.sql (RUYI-359 consolidation)

-- Runtime skill discovery index: a workspace-scoped mirror of the skills each
-- connected runtime's local discovery surfaced (see
-- server/internal/daemon/local_skills.go). Rows are metadata-only summaries —
-- the authoritative skill bodies stay in `skill`, written by the existing
-- import flow when a discovered skill is actually brought into the workspace.
-- No foreign keys by repo rule; workspace teardown deletes these rows
-- explicitly in DeleteWorkspaceSquadsAndSkills.
CREATE TABLE runtime_skill_discovery (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    runtime_id TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    root TEXT NOT NULL DEFAULT '',
    plugin TEXT NOT NULL DEFAULT '',
    key TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    source_path TEXT NOT NULL DEFAULT '',
    file_count INT NOT NULL DEFAULT 1,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- >>> absorbed from 971.up.sql (RUYI-359 consolidation)

CREATE UNIQUE INDEX IF NOT EXISTS idx_runtime_skill_discovery_identity
    ON runtime_skill_discovery (workspace_id, runtime_id, key);
