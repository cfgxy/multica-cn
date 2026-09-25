-- History list and diff-by-version both read "all versions of this scope
-- entity, newest first".
CREATE INDEX CONCURRENTLY idx_prompt_version_scope_created
    ON prompt_version (scope, scope_id, created_at DESC);
