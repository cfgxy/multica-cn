ALTER TABLE issue_dependency DROP CONSTRAINT issue_dependency_type_check;

ALTER TABLE issue_dependency
    ADD CONSTRAINT issue_dependency_type_check
    CHECK (type IN ('blocks', 'blocked_by', 'related'));

ALTER TABLE issue_dependency DROP COLUMN created_at;
