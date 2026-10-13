-- RUYI-751 down: drop the clause asset family and restore the pre-970
-- prompt_proposal shape. The four-phase migration ladder applies this stem
-- only on databases that opted into phase 1, so dropping is unconditional.

DROP INDEX IF EXISTS idx_clause_quality_daily_workspace;
DROP INDEX IF EXISTS idx_clause_quality_daily_clause_day;
DROP TABLE IF EXISTS clause_quality_rollup_state;
DROP TABLE IF EXISTS clause_quality_daily;

DROP INDEX IF EXISTS idx_clause_injection_workspace_time;
DROP INDEX IF EXISTS idx_clause_injection_clause_time;
DROP TABLE IF EXISTS clause_injection;

DROP INDEX IF EXISTS idx_clause_binding_workspace_active;
DROP INDEX IF EXISTS idx_clause_binding_scope_active;
DROP INDEX IF EXISTS uidx_clause_binding_active_clause;
DROP TABLE IF EXISTS clause_binding;

DROP TABLE IF EXISTS clause_version;
DROP TABLE IF EXISTS clause;

ALTER TABLE prompt_proposal DROP CONSTRAINT IF EXISTS prompt_proposal_source_check;
ALTER TABLE prompt_proposal ADD CONSTRAINT prompt_proposal_source_check
    CHECK (source IN ('manual', 'retrospective'));
ALTER TABLE prompt_proposal DROP CONSTRAINT IF EXISTS prompt_proposal_change_kind_check;
ALTER TABLE prompt_proposal ADD CONSTRAINT prompt_proposal_change_kind_check
    CHECK (change_kind IN ('add_clause', 'revise_clause', 'remove_clause'));
ALTER TABLE prompt_proposal DROP CONSTRAINT IF EXISTS prompt_proposal_conditions_shape;
ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS conditions;
ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS rollback_of_version;
ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS move_to_scope_id;
ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS move_to_scope;
ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS clause_id;
