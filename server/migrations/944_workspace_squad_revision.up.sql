-- Optimistic-lock revisions for the two remaining instruction surfaces
-- (RUYI-666): workspace.context (workspace context, the cross-project
-- instruction layer) and squad.instructions (squad instructions). Same
-- contract as the issue/comment/project/agent revision columns (migrations
-- 351/920/928): BIGINT starting at 1, incremented on every successful
-- instruction-bearing update so a client can pass the value it read as
-- expected_revision and have the write fail with a structured
-- revision_conflict instead of silently overwriting.
--
-- The revision guards the instruction-bearing update paths only
-- (UpdateWorkspace / UpdateSquad and the prompt-version activation writes);
-- bookkeeping updates such as issue_counter increments and member-role
-- changes go through other queries and do not bump it.
ALTER TABLE workspace
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE squad
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
