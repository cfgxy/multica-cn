-- RUYI-359 consolidation: absorbs 917, 919, 920, 921, 922, 937 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 937.down.sql (RUYI-359 consolidation)

-- Rolling back the gap backfill removes only the rows it wrote, identified by
-- the change note it stamps. 922's baselines carry a different note and stay,
-- as do every 'edit' and 'revert' version — those are real history.
DELETE FROM prompt_version
WHERE version = 1
  AND source = 'import'
  AND change_note = 'v1 基线：补齐缺失的创建时基线';

-- >>> absorbed from 922.down.sql (RUYI-359 consolidation)

DELETE FROM prompt_version WHERE version = 1 AND source = 'import';

-- >>> absorbed from 921.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_version_workspace;

-- >>> absorbed from 920.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_version_scope_created;

-- >>> absorbed from 919.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_version_scope_version;

-- >>> absorbed from 917.down.sql (RUYI-359 consolidation)

ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS prompt_versions;

-- >>> lead 916.down.sql (drops the structures created above)

-- The three indexes are dropped by the sections above; DROP TABLE also
-- removes any indexes still left.
DROP TABLE IF EXISTS prompt_version;
