-- RUYI-351: widen issue_dependency.type for structured issue relations.
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
