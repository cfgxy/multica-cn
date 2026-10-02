-- Restore the four-tier scope and three-write-source constraints. The down
-- fails by design if any prompt_version row already carries one of the new
-- values: those rows are real history and must not be silently dropped, so
-- downgrade requires migrating the data out first.

ALTER TABLE prompt_version DROP CONSTRAINT prompt_version_source_check;
ALTER TABLE prompt_version
    ADD CONSTRAINT prompt_version_source_check
    CHECK (source IN ('import', 'edit', 'revert', 'auto_snapshot'));

ALTER TABLE prompt_version DROP CONSTRAINT prompt_version_scope_check;
ALTER TABLE prompt_version
    ADD CONSTRAINT prompt_version_scope_check
    CHECK (scope IN ('workspace', 'project', 'squad', 'agent'));
