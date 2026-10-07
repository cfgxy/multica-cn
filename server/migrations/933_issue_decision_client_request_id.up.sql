-- RUYI-514: idempotent decision-card creation. RUYI-495 duplicated a card
-- because a create that actually succeeded looked failed to its caller (the
-- tool output never reached the pipe reader) and the retry was allowed to
-- mint a second row. client_request_id carries the caller's optional
-- idempotency key (the CLI derives one per run+decision); a same-key retry
-- hits the unique index and the handler returns the original card instead of
-- creating a new one. Keyless rows never participate: the partial index
-- excludes them, so callers that opt out keep today's exactly-on-create
-- behavior. Key scope is (workspace, creator type, creator id) — two actors
-- reusing the same key string stay independent.
ALTER TABLE issue_decisions ADD COLUMN client_request_id TEXT;

-- Inlined plain CREATE UNIQUE INDEX, same carve-out as 918 on this same
-- table: column and index land in this file's single implicit transaction,
-- so a build either lands valid or rolls back whole — no INVALID leftover is
-- possible. issue_decisions is a cold, small table and every existing row
-- has a NULL key, so the build cannot meet duplicate data and the brief
-- write lock is negligible. Splitting the index into its own CONCURRENTLY
-- stem would break the one-atomic-deliverable-per-issue numbering rule for
-- no locking benefit.
CREATE UNIQUE INDEX IF NOT EXISTS uidx_issue_decisions_client_request
    ON issue_decisions (workspace_id, created_by_type, created_by_id, client_request_id)
    WHERE client_request_id IS NOT NULL;
