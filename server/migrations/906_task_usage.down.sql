-- Down for 906_task_usage: reverse concatenation of the former migrations
-- 908, 915, 924 (original order, descending), renumbered by the RUYI-359 Phase 2R
-- domain consolidation. Mapping rules:
-- server/cmd/migrate/9xx-consolidation.md.

ALTER TABLE task_message
    DROP COLUMN IF EXISTS is_error;

-- >>> from former migration 915 (RUYI-359 Phase 2R)

ALTER TABLE task_usage
    DROP COLUMN IF EXISTS max_context_tokens;
ALTER TABLE task_usage
    DROP COLUMN IF EXISTS compactions;
ALTER TABLE task_usage
    DROP COLUMN IF EXISTS turns;

-- >>> from former migration 908 (RUYI-359 Phase 2R)

ALTER TABLE task_usage
    DROP COLUMN IF EXISTS context_tokens;
