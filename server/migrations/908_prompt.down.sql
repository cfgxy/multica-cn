-- Down for 908_prompt: reverse concatenation of the former migrations
-- 916, 925, 929, 958, 960 (original order, descending), renumbered by the RUYI-359 Phase 2R
-- domain consolidation. Mapping rules:
-- server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 961.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS uidx_prompt_structure_baseline_carrier;

-- >>> lead 960.down.sql (drops the structures created above)

DROP TABLE IF EXISTS prompt_structure_baseline;

-- >>> from former migration 958 (RUYI-359 Phase 2R)

-- >>> absorbed from 959.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_proposal_workspace_status;

-- >>> lead 958.down.sql (drops the structures created above)

DROP TABLE IF EXISTS prompt_proposal;

-- >>> from former migration 929 (RUYI-359 Phase 2R)

-- >>> absorbed from 956.down.sql (RUYI-359 consolidation)

-- Reverse of the grading columns this file added (originally its own
-- migration; its header mis-cited 950). A stored grade is recomputable only
-- while the answer transcript still exists in task_message, so dropping these
-- is a lossy rollback for measurements whose run has since been garbage
-- collected — acceptable for a column-level rollback, and the reason the up
-- migration is additive (a downgrade never rewrites item rows).

ALTER TABLE prompt_quiz_result
    DROP COLUMN IF EXISTS graded_at,
    DROP COLUMN IF EXISTS score_detail,
    DROP COLUMN IF EXISTS score;

ALTER TABLE prompt_quiz_item
    DROP COLUMN IF EXISTS rubric_checks,
    DROP COLUMN IF EXISTS difficulty,
    DROP COLUMN IF EXISTS tags;

-- >>> absorbed from 938.down.sql (RUYI-359 consolidation)

-- Irreversible cleanup: the rows the up migration deleted were orphans whose
-- workspace no longer exists, so there is nowhere to restore them from.
-- Down is intentionally a no-op.

-- >>> absorbed from 936.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quiz_result_item;

-- >>> absorbed from 935.down.sql (RUYI-359 consolidation)

ALTER TABLE prompt_quiz_item
    DROP COLUMN IF EXISTS rubric;

COMMENT ON COLUMN prompt_quiz_item.body IS NULL;

-- >>> absorbed from 934.down.sql (RUYI-359 consolidation)

ALTER TABLE prompt_quiz_result
    DROP COLUMN IF EXISTS runtime_id,
    DROP COLUMN IF EXISTS run_model;

-- >>> absorbed from 933.down.sql (RUYI-359 consolidation)

-- Restore migration 929's outcome vocabulary.
ALTER TABLE prompt_quiz_result
    DROP CONSTRAINT IF EXISTS prompt_quiz_result_outcome_check;

UPDATE prompt_quiz_result SET outcome = 'passed' WHERE outcome = 'answered';

ALTER TABLE prompt_quiz_result
    ADD CONSTRAINT prompt_quiz_result_outcome_check
    CHECK (outcome IN ('passed', 'failed', 'errored'));

COMMENT ON COLUMN prompt_quiz_result.outcome IS NULL;

-- >>> absorbed from 932.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quiz_item_workspace;

-- >>> absorbed from 931.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quiz_result_batch;

-- >>> absorbed from 930.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quiz_result_baseline;

-- >>> lead 929.down.sql (drops the structures created above)

DROP TABLE IF EXISTS prompt_quiz_sweep_state;
DROP TABLE IF EXISTS prompt_quiz_result;
DROP TABLE IF EXISTS prompt_quiz_item;

-- >>> from former migration 925 (RUYI-359 Phase 2R)

-- >>> absorbed from 928.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_perplexity_score_workspace;

-- >>> absorbed from 927.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quality_daily_workspace;

-- >>> absorbed from 926.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quality_daily_scope_day;

-- >>> lead 925.down.sql (drops the structures created above)

DROP TABLE IF EXISTS prompt_quality_rollup_state;
DROP TABLE IF EXISTS prompt_perplexity_score;
DROP TABLE IF EXISTS prompt_quality_daily;

-- >>> from former migration 916 (RUYI-359 Phase 2R)

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
