-- One row per (scope, scope_id, version): the invariant every version
-- assignment relies on. Version numbers are assigned under a row lock on
-- the owning entity (workspace/project/squad/agent), so a race that still
-- got past that lock fails here instead of creating a duplicate version.
CREATE UNIQUE INDEX CONCURRENTLY idx_prompt_version_scope_version
    ON prompt_version (scope, scope_id, version);
