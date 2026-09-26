# Migration runner operations

## Recover the comment content search index

Migration 371 keeps exactly one comment-content search index per environment:
`idx_comment_content_bigm` when `pg_bigm` is usable, otherwise the portable
`idx_comment_content_trgm` fallback. A conditionally skipped migration is still
recorded in `schema_migrations`, so rerunning `migrate up` does not recreate the
fallback if the selected bigram index is later dropped or becomes invalid.

First check whether either index is live, ready, and valid:

```sql
SELECT indexrelid::regclass AS index_name, indisvalid, indisready, indislive
FROM pg_index
WHERE indexrelid IN (
    to_regclass('idx_comment_content_bigm'),
    to_regclass('idx_comment_content_trgm')
);
```

If neither index is usable, restore the portable fallback before serving search
traffic. Run each statement separately and outside a transaction so the
concurrent index build is valid:

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
DROP INDEX CONCURRENTLY IF EXISTS idx_comment_content_trgm;
CREATE INDEX CONCURRENTLY idx_comment_content_trgm
    ON comment USING gin (LOWER(content) gin_trgm_ops);
```

Verify that `idx_comment_content_trgm` reports all three flags as `true` before
resuming traffic. If `idx_comment_content_bigm` is repaired later, keep the
fallback until the bigram index also reports all three flags as `true` **and**
has the exact migration 036 shape: a non-unique, non-partial GIN index on
`LOWER(content)` using the `pg_bigm`-owned `gin_bigm_ops` operator class. Only
then can the fallback be dropped with `DROP INDEX CONCURRENTLY` during a
maintenance window.

## Build the issue properties bigram index after installing pg_bigm

Migration 446 builds `idx_issue_properties_bigm`, the index behind the prefilter
that scalar `contains` property filtering puts in front of its per-key ILIKE.
The runner only executes it where the `gin_bigm_ops` operator class is
installed; everywhere else the version is recorded with its SQL skipped, and the
filter keeps working without index acceleration.

That record is permanent, so a database that gains `pg_bigm` later never builds
the index on its own. Check first:

```sql
SELECT indexrelid::regclass AS index_name, indisvalid, indisready, indislive
FROM pg_index
WHERE indexrelid = to_regclass('idx_issue_properties_bigm');
```

If the index is missing, create it out of band. Run each statement separately
and outside a transaction so the concurrent build is valid:

```sql
CREATE EXTENSION IF NOT EXISTS pg_bigm;
DROP INDEX CONCURRENTLY IF EXISTS idx_issue_properties_bigm;
CREATE INDEX CONCURRENTLY idx_issue_properties_bigm
    ON issue USING gin (LOWER(properties::text) gin_bigm_ops);
ANALYZE issue;
```

The `ANALYZE` is not optional and not a formality. Building an expression index
does not collect statistics for its expression, and until they exist the
planner has nothing to judge the index by: it falls back to a pattern-length
heuristic that estimates a single-character needle at 5% of the table and
leaves `contains` on a sequential scan, with the index built, valid and unused.
Migration 447 does this after 446; an out-of-band build has to do it itself.

Keep the expression exactly as written: the predicate is
`LOWER(properties::text) LIKE LOWER(...)`, and an `ILIKE`-shaped or
non-lowered index would never be used (pg_bigm 1.2 on RDS has no ILIKE index
scan — the constraint migration 036 hit).

Confirm the statistics landed:

```sql
SELECT count(*) FROM pg_statistic WHERE starelid = 'idx_issue_properties_bigm'::regclass;
```

## Renumber a migration

Renumbering a migration file is allowed. This fork and upstream advance in
parallel, so a migration that was written against one numbering lands on top of
numbers that upstream has since taken, and renumbering is the normal way to
reconcile the two.

What is not allowed is rolling the migration back and reapplying it under the
new number. `schema_migrations` records a migration by its file stem, and the
runner's per-file `EXISTS` check in `runMigrations` uses that stem verbatim
(`server/cmd/migrate/main.go`). After a rename the stale row no longer matches
any file, so a `down` run skips the migration it should remove and an `up` run
replays DDL that has already been applied. `901_project_instructions` is the
worked example: its `down` dropped `project.instructions`, and the replay under
the new number recreated the column empty, losing the prompt content of 17
projects (RUYI-212).

The correct procedure is to renumber the file and rewrite the run record to
match it. Nothing is rolled back and nothing is replayed.

### 1. Rename both directions

```bash
git mv server/migrations/441_project_instructions.up.sql   server/migrations/901_project_instructions.up.sql
git mv server/migrations/441_project_instructions.down.sql server/migrations/901_project_instructions.down.sql
```

Keep the name part — everything after the numeric prefix — byte-identical. The
name part is the only thing that survives a renumber, so it is what identifies
the stale ledger row. `TestMigrationNamePartsAreGloballyUnique` fails the build
if two migrations ever share one, because a duplicate makes that lookup
ambiguous.

### 2. Ask the runner what the ledger needs

```bash
cd server && DATABASE_URL=... go run ./cmd/migrate check-ledger
```

`check-ledger` is read-only. It reads `schema_migrations`, compares it with the
files on disk, and exits non-zero with the exact statements to run when it finds
a row recorded under a version that no longer exists. Rows from other branches
and not-yet-applied files are listed for context and do not fail the check — on
a shared development database both are normal.

### 3. Run the statements it printed

The statements are parameter-free and can be pasted as-is:

```sql
-- the usual case: re-point the surviving row at the new number
UPDATE schema_migrations SET version = '901_project_instructions' WHERE version = '441_project_instructions';

-- when the migration was already replayed under its new number, the stale row
-- is redundant and is removed instead
DELETE FROM schema_migrations WHERE version = '441_project_instructions';
```

`UPDATE` is the default. Never `DELETE` a row whose new version is absent: that
is exactly the state in which the next `up` run replays the migration.

### 4. Confirm

```bash
cd server && DATABASE_URL=... go run ./cmd/migrate check-ledger
```

Expect `schema_migrations matches the migration files on disk.` and exit 0.

Every environment that already applied the old number needs steps 2–4 run
against it — the ledger lives in each database, not in the repository.

### Choose the number for a new migration

The prefix gate only sees the files in this checkout, so the next free number on
disk can still be taken by a branch that has already merged elsewhere and
applied its migration to the shared development database. Run `check-ledger`
before picking a number and read its "match no file in this checkout" list: it
shows the versions other branches have already recorded. Start above all of
them. `937_prompt_version_v1_gap_backfill` was written as `933_...` and
renumbered for exactly this reason — the shared ledger already carried
`933_prompt_quiz_outcome_answered` through `936_prompt_quiz_result_item_index`.
Renumbering a file that no environment has applied yet costs nothing; leaving
the collision in would break the prefix gate on merge.

### Existing collisions

Two invariants are gated in `server/internal/migrations/migrations_lint_test.go`
and hold for anything new:

- numeric prefixes are unique, except for the frozen historical set in
  `legacyDuplicateMigrationStems`, which a new collision must not be added to;
- name parts are globally unique, with no exemption list.

The duplicate prefixes below 148 are the frozen legacy set and are left as they
are: they are all long applied everywhere, renumbering them would require the
procedure above on every environment including production, and the prefix
collision alone causes no replay — only a name-part collision does, and there
are none. New migrations start at 149 and the prefix gate keeps them unique, so
the set does not grow.
