-- RUYI-359 consolidation: absorbs 930, 931, 932, 933, 934, 935, 936, 938, 956 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 929.up.sql (RUYI-359 consolidation)

-- Prompt quiz bank and measurements (RUYI-185, self-evolution phase 3): a
-- fixed set of questions re-run against every prompt version so the quality
-- numbers of two versions are comparable, plus the periodic re-run that
-- surfaces regressions.
--
-- TWO NEW ENTITIES, NOT THREE:
--   ADR-002 §4 proposed evolve_quiz_item / evolve_quiz_run / evolve_quiz_result.
--   The run entity is not created here. A quiz run IS an agent run: it goes
--   through agent_task_queue on the existing issue_id IS NULL path (the one
--   chat and quick-create already use), tagged originator_source='quiz'. That
--   buys claim, retry, timeout, cancellation, usage accounting and — decisively
--   — prompt_versions attribution (migration 916) for free. A separate run
--   table would have to restate all of it and would still not be the row the
--   daemon claims. The batch a run belongs to is a COLUMN on the result
--   (batch_id), not a table: nothing is ever read about a batch that is not
--   derivable from its member rows.
--
-- WHY ITEM DEFINITION AND ITEM VERSION ARE NOT SPLIT:
--   The only reason to keep an item's history is so an old measurement stays
--   interpretable after the wording changed. prompt_quiz_result carries
--   item_revision and item_body_sha256 at measurement time, so each row is
--   self-describing and old rows stay readable without the old body. Nothing
--   reads a superseded body: a measurement taken against different wording is
--   not comparable and is excluded by revision, not re-displayed. A version
--   table would exist to serve no query.
--
-- WHY RESULTS DO NOT LAND IN prompt_quality_daily:
--   The phase-3 ruling is that a regression baseline is a DISTRIBUTION, not a
--   scalar: the decision compares the new version's sample GROUP against the
--   baseline sample GROUP. prompt_quality_daily is keyed
--   (scope, scope_id, version, day) and stores a median — the per-sample
--   values it folded away are exactly what the comparison needs, and no
--   aggregate can give them back. So this is a sibling fact table at
--   one-row-per-measurement grain, and the aggregates the dashboard shows are
--   computed from it rather than stored twice.
--
-- No foreign keys by house rule. Secondary indexes are inlined in this file
-- (RUYI-359 consolidation): they build without CONCURRENTLY because the tables
-- are created in this same implicit transaction (914 precedent).

-- The quiz bank. Bodies are authored by workspace owners and are the ONLY
-- free text a quiz run ever receives.
--
-- ISOLATION IS A CONSTRAINT, NOT A SANDBOX (Owner Q18-A): a quiz run has no
-- issue and must not be able to reach production rows, so the body is barred
-- from naming any — no UUIDs, no issue keys, no mention:// links. That rule is
-- enforced in the application layer (pkg/promptquiz) on every write; it is not
-- expressible as a CHECK without embedding the platform's id grammar in DDL.
CREATE TABLE prompt_quiz_item (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- Stable human handle, used in the dashboard and in operator reports so a
    -- question can be discussed without quoting its body (Owner Q8: bodies do
    -- not leave the platform).
    slug TEXT NOT NULL CHECK (slug <> ''),
    title TEXT NOT NULL CHECK (title <> ''),

    -- The question put to the agent. Bounded so one item cannot dominate the
    -- run's context; the same ceiling the governance gate uses for prompts
    -- would be far too generous for a question.
    body TEXT NOT NULL CHECK (body <> '' AND length(body) <= 4000),

    -- Bumped by the application whenever body changes. Measurements record the
    -- revision they were taken against, which is what makes old rows readable
    -- without keeping old bodies.
    revision INT NOT NULL DEFAULT 1 CHECK (revision > 0),

    -- Which assembled-prompt variant this item is asked against, matching
    -- prompt_perplexity_score.runtime_profile (migration 925). An item written
    -- for the leader-task runtime is meaningless to an ordinary member.
    runtime_profile TEXT NOT NULL DEFAULT 'member'
        CHECK (runtime_profile IN ('member', 'leader_task')),

    -- Retired items stop being scheduled but keep their measurements
    -- interpretable. Deleting an item would silently rewrite the history of
    -- every version measured with it.
    active BOOLEAN NOT NULL DEFAULT TRUE,

    created_by_user_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_prompt_quiz_item_slug UNIQUE (workspace_id, slug)
);

COMMENT ON TABLE prompt_quiz_item IS
    'RUYI-185 quiz bank: the fixed question set replayed against every prompt version. Bodies may not name production entities (enforced in pkg/promptquiz, not in DDL). Item history is carried by prompt_quiz_result.item_revision rather than by a version table.';

-- One measurement: one question, asked once, of one prompt version.
--
-- WHY ONE ROW PER MEASUREMENT AND NOT PER (VERSION, ITEM):
--   N repeats of the same question against the same version is the whole
--   point — the spread ACROSS those repeats is the noise line the regression
--   decision is measured against. Collapsing them would leave the comparison
--   with nothing to be significant relative to.
--
-- task_id is the join back to the run, and therefore to
-- agent_task_queue.prompt_versions. scope / scope_id / version are still
-- stored here rather than resolved through that join on every read: the
-- baseline query groups by them over thousands of rows, and re-expanding a
-- JSONB column per row to do it would make the read path the expensive thing
-- the phase-2 rollup exists to avoid.
CREATE TABLE prompt_quiz_result (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'project', 'squad', 'agent')),
    scope_id UUID NOT NULL,
    version INT NOT NULL CHECK (version > 0),

    item_id UUID NOT NULL,
    item_revision INT NOT NULL CHECK (item_revision > 0),

    -- Body digest at measurement time. Two rows with the same item_revision but
    -- different digests mean the revision counter was not bumped on an edit;
    -- the baseline reader treats a digest mismatch as "not comparable" rather
    -- than trusting the counter.
    item_body_sha256 TEXT NOT NULL CHECK (item_body_sha256 <> ''),

    -- The measurement batch. A column, not a table: a batch has no attribute
    -- that is not min()/max()/count() over its own rows.
    batch_id UUID NOT NULL,

    -- The agent_task_queue row that produced this measurement. UNIQUE because a
    -- run answers exactly one item, which also makes result collection
    -- idempotent under scheduler re-entry: the collector upserts on it.
    task_id UUID NOT NULL,

    -- Whether the run produced an answer at all; errored is the run that did
    -- not (timeout, provider outage, cancellation) and is NOT a failure of the
    -- prompt, the same split prompt_quality_daily makes for D6. No correctness
    -- judgement is made anywhere, so neither value is a grade — the outcome
    -- narrowing below (originally its own migration) is the authority on it.
    outcome TEXT NOT NULL CHECK (outcome IN ('passed', 'failed', 'errored')),

    -- Per-measurement cost, the dimension the distribution comparison runs on.
    -- NULL means the daemon reported no usage for the run — "not measured",
    -- which the baseline drops rather than reading as zero.
    run_tokens BIGINT CHECK (run_tokens IS NULL OR run_tokens >= 0),

    -- Wall-clock cost of the same measurement, kept so a version that got
    -- cheaper by getting slower cannot read as a pure improvement.
    duration_ms BIGINT CHECK (duration_ms IS NULL OR duration_ms >= 0),

    measured_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_prompt_quiz_result_task UNIQUE (task_id)
);

COMMENT ON TABLE prompt_quiz_result IS
    'RUYI-185 quiz measurements at one-row-per-repeat grain — the grain the distribution baseline needs and prompt_quality_daily cannot express. batch_id is a column because a batch has no fact of its own; task_id joins back to the quiz run in agent_task_queue (originator_source=quiz, issue_id IS NULL).';

-- Single-row watermark for the periodic quiz job, same shape as
-- prompt_quality_rollup_state (migration 925). The job is idempotent and
-- serialised by the scheduler advisory lock; this row exists so an operator
-- can see when the last sweep ran and why it stopped, not to gate it.
CREATE TABLE prompt_quiz_sweep_state (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_run_started_at TIMESTAMPTZ,
    last_run_finished_at TIMESTAMPTZ,
    last_enqueued INT NOT NULL DEFAULT 0,
    last_collected INT NOT NULL DEFAULT 0,
    last_error TEXT
);
INSERT INTO prompt_quiz_sweep_state (id) VALUES (1) ON CONFLICT DO NOTHING;

-- >>> absorbed from 930.up.sql (RUYI-359 consolidation)

-- The baseline comparison's only read shape: every measurement of one scope at
-- one version, for one item revision, ordered by when it was taken. The unique
-- constraint from 929 is on task_id and serves none of it.
--
-- item_id sits after version because the comparison first pins the two version
-- groups and then walks the items within them; measured_at last so a "latest N
-- repeats" window is a range scan rather than a sort.
CREATE INDEX IF NOT EXISTS idx_prompt_quiz_result_baseline
    ON prompt_quiz_result (scope, scope_id, version, item_id, measured_at DESC);

-- >>> absorbed from 931.up.sql (RUYI-359 consolidation)

-- Batch progress: the periodic job asks "is this sweep's batch finished" and
-- the dashboard shows a batch's outcome breakdown. Both read one batch_id.
CREATE INDEX IF NOT EXISTS idx_prompt_quiz_result_batch
    ON prompt_quiz_result (batch_id);

-- >>> absorbed from 932.up.sql (RUYI-359 consolidation)

-- Bank maintenance and sweep enumeration both list one workspace's items. The
-- unique constraint from 929 leads with workspace_id but carries slug, so it
-- serves an exact-slug lookup and not an ordered listing.
CREATE INDEX IF NOT EXISTS idx_prompt_quiz_item_workspace
    ON prompt_quiz_item (workspace_id, active, created_at DESC);

-- >>> absorbed from 933.up.sql (RUYI-359 consolidation)

-- Narrow prompt_quiz_result.outcome to what the code actually measures
-- (RUYI-185 review, blocking item 2).
--
-- Migration 929 declared 'passed' / 'failed' as "the graded outcomes", but
-- nothing grades a quiz answer: the only writer maps a terminal
-- agent_task_queue status onto an outcome, so 'passed' meant "the run reached
-- status completed" and 'failed' had no writer at all. A dashboard reading
-- "passed 12 / 12" as a pass RATE would be reporting a number nobody computed.
--
-- The vocabulary is therefore narrowed to the distinction the mechanism can
-- actually make: did the run produce an answer, or did it produce nothing.
-- Grading an answer against a rubric is a separate judgement; when it lands it
-- gets its own column rather than reusing this one, because "the run finished"
-- and "the answer was correct" are independent facts and one value cannot
-- carry both.
--
-- 'passed' rows are rewritten rather than kept as a legal value: they were
-- written by OutcomeForStatus with exactly the meaning 'answered' now names, so
-- there is no old row whose meaning this rename changes.
ALTER TABLE prompt_quiz_result
    DROP CONSTRAINT IF EXISTS prompt_quiz_result_outcome_check;

UPDATE prompt_quiz_result SET outcome = 'answered' WHERE outcome = 'passed';

-- 'failed' had no writer, so there is nothing to migrate; the value is simply
-- gone from the vocabulary. Should a row exist in a database this project does
-- not know about, the constraint below rejects it and the operator sees why
-- rather than the value surviving as a silent third meaning.
ALTER TABLE prompt_quiz_result
    ADD CONSTRAINT prompt_quiz_result_outcome_check
    CHECK (outcome IN ('answered', 'errored'));

COMMENT ON COLUMN prompt_quiz_result.outcome IS
    'answered = the run produced an answer to the question; errored = it produced none (timeout, provider outage, cancellation). This is NOT a grade: no correctness judgement is made anywhere in RUYI-185, so neither value may be presented as a pass rate. Only answered rows with a measured run_tokens enter the distribution sample.';

-- >>> absorbed from 934.up.sql (RUYI-359 consolidation)

-- Pin the runtime a measurement was taken on (RUYI-185 review, A1).
--
-- A quiz reading is only comparable to another reading taken THE SAME WAY. The
-- question, its wording and the prompt version were already pinned on every
-- row; the runtime was not, so switching an agent from one runtime or model to
-- another would silently continue the same curve with a different measuring
-- instrument, and the version diff would read as a prompt change.
--
-- WHY THE EXECUTED PAIR AND NOT execution_profile_id:
--   Migration 904 records that workspace.active_execution_profile_id is
--   'Display state only: it records which profile last wrote the agents'
--   runtime/model/thinking_level, not a live binding'. A profile therefore
--   cannot answer "what actually ran this"; the runtime row the queue claimed
--   and the model the daemon reported usage for can.
--
-- Both columns are nullable, and neither is a valid measurement input:
--   runtime_id is NULL only for a row written before this migration, because
--   migration 251 made agent_task_queue.runtime_id nullable on terminal rows
--   and the collector now SKIPS such a run — a run whose runtime is unknown
--   cannot be placed in any cohort.
--   run_model is NULL when the daemon reported no usage for the run. Such a run
--   also has no run_tokens, so it never enters a sample either way.
-- The reader treats a NULL in either column as its own cohort key rather than
-- as a wildcard, so an unattributed row can never be folded in with an
-- attributed one.
ALTER TABLE prompt_quiz_result
    ADD COLUMN IF NOT EXISTS runtime_id UUID,
    ADD COLUMN IF NOT EXISTS run_model TEXT;

COMMENT ON COLUMN prompt_quiz_result.runtime_id IS
    'The agent_runtime row the measuring run was claimed with. Part of the cohort key: readings from two runtimes are never merged into one sample group. NULL means unattributed and therefore not comparable.';

COMMENT ON COLUMN prompt_quiz_result.run_model IS
    'The model(s) the daemon reported usage under for the measuring run, comma-joined when a run spanned more than one. Part of the cohort key for the same reason as runtime_id. NULL means the daemon reported no usage, which also leaves run_tokens NULL.';

-- >>> absorbed from 935.up.sql (RUYI-359 consolidation)

-- Split a quiz item into a public half and a private half (RUYI-185 review, A2).
--
-- body is the PUBLIC half: it is the only free text a measuring run receives.
-- rubric is the PRIVATE half: the expected answer and the grading points. It
-- exists so a later grading pass has something to grade against, and it must
-- never reach the run being measured — an agent handed the answer key would
-- answer from it instead of from its prompt, and the reading would measure the
-- leak rather than the prompt.
--
-- WHY A COLUMN AND NOT A SEPARATE TABLE:
--   A rubric has no identity, no lifecycle and no cardinality of its own: it is
--   one text per item, edited with the item, deleted with the item. The
--   containment that matters is not physical storage, it is which query
--   selects it — the sweep's task payload is built from named columns and the
--   member-visible list response omits it, both asserted by test rather than by
--   convention.
--
-- Same 4000-byte ceiling as body, and the same application-layer isolation gate:
-- a rubric that names a production entity would carry the same contamination
-- into the grading side that the gate keeps out of the question side.
ALTER TABLE prompt_quiz_item
    ADD COLUMN IF NOT EXISTS rubric TEXT NOT NULL DEFAULT ''
        CHECK (length(rubric) <= 4000);

COMMENT ON COLUMN prompt_quiz_item.rubric IS
    'Private half of the item: expected answer and grading points. Never sent to a measuring run and never returned by the member-visible bank list. Empty string means no rubric has been written yet, which is why it is not NULL-able.';

COMMENT ON COLUMN prompt_quiz_item.body IS
    'Public half of the item: the question text, and the only free text a measuring run receives. Revision advances when this changes, because a reading taken against different wording is not comparable to one taken against the old wording. Editing the rubric does NOT advance revision: the private half is not part of what was measured.';

-- >>> absorbed from 936.up.sql (RUYI-359 consolidation)

-- The bank maintenance read shape (A4): every measurement of one workspace's
-- questions, grouped per question, newest first.
--
-- Not served by idx_prompt_quiz_result_baseline: that index leads with
-- (scope, scope_id, version), and the discrimination mark asks about a question
-- ACROSS versions and scopes — the whole point is whether the question has ever
-- separated anything. Without this index the bank panel scans every measurement
-- row on the deployment to filter one workspace's.
CREATE INDEX IF NOT EXISTS idx_prompt_quiz_result_item
    ON prompt_quiz_result (workspace_id, item_id, measured_at DESC);

-- >>> absorbed from 938.up.sql (RUYI-359 consolidation)

-- Remove prompt-quiz rows whose workspace no longer exists (RUYI-185 QA S5).
--
-- The workspace-delete query gained the quiz cleanup CTEs at the source, but
-- the sqlc artifact was committed stale: the delete the server actually runs
-- never included prompt_quiz_result / prompt_quiz_item, so every workspace
-- deleted since the quiz shipped left its bank and measurements behind as
-- orphan rows. The query source is authoritative and already fixed; this
-- statement removes what accumulated in databases where the incomplete delete
-- already ran.
--
-- No foreign keys exist by house rule, so the database never cascaded; the
-- results are removed before the bank for readability only. Idempotent: a
-- second run finds no workspace-less rows.
DELETE FROM prompt_quiz_result AS r
WHERE NOT EXISTS (SELECT 1 FROM workspace w WHERE w.id = r.workspace_id);

DELETE FROM prompt_quiz_item AS i
WHERE NOT EXISTS (SELECT 1 FROM workspace w WHERE w.id = i.workspace_id);

-- >>> absorbed from 956.up.sql (RUYI-359 consolidation)

-- Grading pass for the prompt quiz (RUYI-286): the score columns on the
-- measurement, and the structured half of the answer key on the item.
--
-- The outcome narrowing above reserved this landing spot in advance: outcome was
-- narrowed to answered/errored because "the run finished" and "the answer was
-- correct" are independent facts, and its comment committed to giving grading
-- "its own column rather than reusing this one". These are those columns.
--
-- ON prompt_quiz_item:
--   tags / difficulty are bank presentation metadata and are member-visible —
--   they say what a question covers and how hard it is, which is exactly what
--   the bank list is for. Eight coverage categories are carried as tags
--   (discipline / conflict / boundary / tool / format / context / refusal /
--   regression), which is what makes the bank's type coverage mechanically
--   checkable instead of a claim in a comment.
--   rubric_checks is the STRUCTURED half of the answer key: machine-checkable
--   assertions evaluated against the run's answer at collection time. It is
--   private exactly like the rubric column above — it names what a correct
--   answer must and must not contain, so a measured run that saw it would
--   answer from the key rather than from its prompt. The containment is the
--   same named-column discipline that already works for rubric: the member
--   list and the enqueue payload select columns by name, and the Go row types
--   those queries generate have no field for it, so the key cannot leak by a
--   column being added to the table.
--   The free-text rubric stays and keeps its meaning: checks grade, rubric
--   explains. An item with no checks is simply never scored — its results keep
--   score NULL, which reads as "not graded", never as 0.
--
-- ON prompt_quiz_result:
--   score is the weighted pass ratio over the item's rubric_checks at grading
--   time, 0..1. NULL = not graded: the item has no checks, the run left no
--   answer text, or the run errored. It is a separate fact from outcome by
--   construction — an errored run can have no score, and an answered run
--   against a check-less item has none either.
--   score_detail carries the per-assertion verdicts with their evidence: the
--   explanation a reader re-traces the grade from. The answer text itself is
--   deliberately NOT copied here — it stays in task_message, joined via
--   task_id, so the run's transcript remains the single source of truth for
--   what the agent actually said.
--   graded_at is when the grade was computed. measured_at is when the
--   measurement row was stored; both happen in the same collection tick today
--   but they are independent facts and a re-collection rewrites both.

ALTER TABLE prompt_quiz_item
    ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS difficulty TEXT NOT NULL DEFAULT 'medium'
        CHECK (difficulty IN ('easy', 'medium', 'hard')),
    ADD COLUMN IF NOT EXISTS rubric_checks JSONB
        CHECK (rubric_checks IS NULL OR jsonb_typeof(rubric_checks) = 'array');

ALTER TABLE prompt_quiz_result
    ADD COLUMN IF NOT EXISTS score REAL
        CHECK (score IS NULL OR (score >= 0 AND score <= 1)),
    ADD COLUMN IF NOT EXISTS score_detail JSONB,
    ADD COLUMN IF NOT EXISTS graded_at TIMESTAMPTZ;

COMMENT ON COLUMN prompt_quiz_item.tags IS
    'Bank presentation metadata, member-visible. One entry per coverage category (discipline / conflict / boundary / tool / format / context / refusal / regression), which is what makes type coverage mechanically checkable.';

COMMENT ON COLUMN prompt_quiz_item.difficulty IS
    'Bank presentation metadata, member-visible: easy / medium / hard.';

COMMENT ON COLUMN prompt_quiz_item.rubric_checks IS
    'Structured half of the answer key: machine-checkable assertions graded against the run answer at collection time. Private like rubric (migration 935): never sent to a measuring run, never returned by the member-visible list. NULL or [] = the item is not scored.';

COMMENT ON COLUMN prompt_quiz_result.score IS
    'Weighted pass ratio over the item''s rubric_checks, 0..1, computed at collection. NULL = not graded: no checks on the item, no answer text, or an errored run. Independent of outcome by design (migration 933).';

COMMENT ON COLUMN prompt_quiz_result.score_detail IS
    'Per-assertion verdicts with evidence — the explanation the grade can be re-traced from. Evidence names the assertion and the verdict, never quotes the answer; the answer stays in task_message (join via task_id).';

COMMENT ON COLUMN prompt_quiz_result.graded_at IS
    'When the grade was computed (collection tick). measured_at is when the measurement row was stored; grading happens in the same tick today but the two are independent facts.';
