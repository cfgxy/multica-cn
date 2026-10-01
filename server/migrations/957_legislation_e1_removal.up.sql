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
