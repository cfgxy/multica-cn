-- RUYI-359 Phase 2R domain consolidation: merges the former migrations
-- 941, 957, 970 into one atomic migration (renumbered to 913_skill) on the gap-free
-- 900+ ladder. Statement bodies are unchanged except CREATE/DROP INDEX lost
-- the CONCURRENTLY keyword: every target is created earlier in this same
-- file or by an earlier migration, and the whole file runs as one implicit
-- transaction (914 precedent); existing environments converge via the
-- ledger rewrite and never re-run these files. Original-stem -> new-stem
-- mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 941.up.sql (RUYI-359 consolidation)

CREATE TABLE IF NOT EXISTS skill_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    skill_id UUID NOT NULL,
    version INT NOT NULL CHECK (version > 0),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    files JSONB NOT NULL DEFAULT '[]'::jsonb,
    source TEXT NOT NULL CHECK (source IN ('create', 'proposal', 'revision', 'edit', 'restore')),
    source_version INT,
    source_proposal_id UUID,
    author_user_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO skill_version (workspace_id, skill_id, version, name, description, content,
                           config, files, source, author_user_id, created_at)
SELECT s.workspace_id, s.id, 1, s.name, s.description, s.content, s.config,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('path', f.path, 'content', f.content)
                              ORDER BY f.path)
                 FROM skill_file f WHERE f.skill_id = s.id), '[]'::jsonb),
       'create', s.created_by, s.created_at
FROM skill s
WHERE NOT EXISTS (SELECT 1 FROM skill_version v WHERE v.skill_id = s.id);

-- >>> absorbed from 942.up.sql (RUYI-359 consolidation)

CREATE UNIQUE INDEX idx_skill_version_identity
ON skill_version (skill_id, version);

-- >>> from former migration 957 (RUYI-359 Phase 2R)

-- RUYI-305 E1: the behavior-prophecy proposal pool is dismantled (Owner
-- decision 1A on RUYI-298, 2026-09-30). The old `proposal` table carried
-- falsifiable prophecies (941-era), adopt/verify semantics and the knowledge
-- transfer path — none of which is Prompt legislation. The replacement
-- pool is a new table (prompt_proposal, 958) with a different schema, so
-- the old one is dropped outright; its rows are dev-stage experiment data
-- and are not migrated.
--
-- skill_version.source = 'proposal' (941) was the link the old adopt flow
-- wrote; the pool that produced it no longer exists. Rows carrying it are
-- re-labelled 'edit' before the CHECK shrinks so the constraint swap cannot
-- fail on leftover data; source_proposal_id is dropped with the link.
DROP TABLE IF EXISTS proposal;

UPDATE skill_version SET source = 'edit' WHERE source = 'proposal';

ALTER TABLE skill_version DROP CONSTRAINT IF EXISTS skill_version_source_check;
ALTER TABLE skill_version ADD CONSTRAINT skill_version_source_check
    CHECK (source IN ('create', 'revision', 'edit', 'restore'));
ALTER TABLE skill_version DROP COLUMN IF EXISTS source_proposal_id;

-- >>> from former migration 970 (RUYI-359 Phase 2R)

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
