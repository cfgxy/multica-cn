-- RUYI-351: strict reverse of 919_issue_dependency_relationships.up.sql
-- (consolidated from the two RUYI-351 issue-dependency migrations). Single
-- implicit transaction, so per 9xx-consolidation.md §2.1 the CONCURRENTLY
-- keyword is omitted.
DROP INDEX IF EXISTS uq_issue_dependency_edge;

ALTER TABLE issue_dependency DROP COLUMN created_at;

ALTER TABLE issue_dependency DROP CONSTRAINT issue_dependency_type_check;

ALTER TABLE issue_dependency
    ADD CONSTRAINT issue_dependency_type_check
    CHECK (type IN ('blocks', 'blocked_by', 'related'));
