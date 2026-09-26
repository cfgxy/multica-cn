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
--   — prompt_versions attribution (migration 917) for free. A separate run
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
-- No foreign keys by house rule. Indexes are not created here: every index
-- must be CONCURRENTLY, which cannot run in a multi-statement file — see
-- 930 / 931 / 932.

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

    -- passed / failed are the graded outcomes. errored is the run that never
    -- produced an answer (timeout, provider outage, cancellation) and is NOT a
    -- failure of the prompt: it is excluded from the graded rate and reported
    -- separately, the same split prompt_quality_daily makes for D6.
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
