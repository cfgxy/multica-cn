-- Optimistic-lock revisions for the execution-config surfaces (RUYI-433).
-- Same contract as the issue/comment/project revision columns (migrations
-- 351/920): BIGINT starting at 1, incremented on every successful update so
-- a client can pass the value it read as expected_revision and have the
-- write fail with a structured revision_conflict instead of silently
-- overwriting.
--
-- Scope: on `agent` the revision guards execution-configuration writes
-- (PATCH /api/agents/{id} and execution-profile activation, the two paths
-- that move runtime_id / model / thinking_level). Status transitions and
-- task bookkeeping go through other queries and do not bump it, so a
-- concurrent status change never invalidates a config-write token.
ALTER TABLE agent
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE execution_profile
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
