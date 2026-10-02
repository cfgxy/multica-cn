-- RUYI-359 consolidation: absorbs 942 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


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
