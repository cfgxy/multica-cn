-- RUYI-351: widen issue_dependency.type for structured issue relations and
-- give each relation edge a unique identity, consolidated into one migration
-- from the two RUYI-351 issue-dependency migrations (one 9xx slot per
-- issue). Statements run as a single implicit transaction,
-- so the index build drops CONCURRENTLY per the 9xx-consolidation.md §2.1
-- mechanical rule; that is safe here because issue_dependency has stored no
-- rows since 001, making the build instantaneous.
-- The table has stored nothing since 001 (no code path ever wrote to it),
-- so re-defining the vocabulary touches no rows. Relations store one
-- canonical row per edge: 'blocks' and 'supersedes' keep only the forward
-- direction ('blocked_by' / 'superseded_by' are query-side inverses), and
-- the symmetric 'relates_to' stores one row per unordered issue pair. The
-- legacy values stay valid so any stray historical row still reads back;
-- current code never writes them.
ALTER TABLE issue_dependency DROP CONSTRAINT issue_dependency_type_check;

ALTER TABLE issue_dependency
    ADD CONSTRAINT issue_dependency_type_check
    CHECK (type IN ('blocks', 'blocked_by', 'related', 'relates_to', 'supersedes'));

-- Creation timestamps: relation lists order chronologically. Backfilled by
-- the default for the zero existing rows.
ALTER TABLE issue_dependency ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE UNIQUE INDEX IF NOT EXISTS uq_issue_dependency_edge
    ON issue_dependency(issue_id, depends_on_issue_id, type);
