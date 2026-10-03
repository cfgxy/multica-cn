-- RUYI-285 rework: prompt versioning covers every prompt-carrying entity —
-- the four tiers (workspace/project/squad/agent) plus autopilot run prompts
-- and skill bodies. Both new scope values are read through the same
-- lock/entity-row path as the existing tiers.
--
-- 'snapshot' is the manual "capture currently effective content" source the
-- reworked self-evolution page writes: append a version row, never touch the
-- business column. It is distinct from 'auto_snapshot', which stays reserved
-- for automated checkpoints.

ALTER TABLE prompt_version DROP CONSTRAINT prompt_version_scope_check;
ALTER TABLE prompt_version
    ADD CONSTRAINT prompt_version_scope_check
    CHECK (scope IN ('workspace', 'project', 'squad', 'agent', 'autopilot', 'skill'));

ALTER TABLE prompt_version DROP CONSTRAINT prompt_version_source_check;
ALTER TABLE prompt_version
    ADD CONSTRAINT prompt_version_source_check
    CHECK (source IN ('import', 'edit', 'revert', 'auto_snapshot', 'snapshot'));
