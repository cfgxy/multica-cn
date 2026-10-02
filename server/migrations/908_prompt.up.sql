-- RUYI-359 Phase 2R domain consolidation: merges the former migrations
-- 916, 925, 929, 958, 960 into one atomic migration (renumbered to 908_prompt) on the gap-free
-- 900+ ladder. Statement bodies are unchanged except CREATE/DROP INDEX lost
-- the CONCURRENTLY keyword: every target is created earlier in this same
-- file or by an earlier migration, and the whole file runs as one implicit
-- transaction (914 precedent); existing environments converge via the
-- ledger rewrite and never re-run these files. Original-stem -> new-stem
-- mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 916.up.sql (RUYI-359 consolidation)

-- Prompt version history (RUYI-183, self-evolution phase 1): version
-- snapshot management for the four prompt tiers — workspace context,
-- project instructions, squad instructions, agent instructions.
--
-- The four tiers keep their existing plain-TEXT business columns
-- (workspace.context, project.instructions, squad.instructions,
-- agent.instructions) as the single read source for "currently effective
-- content". This table is history and audit only: the injection hot path
-- (daemon task claim, execenv runtime sections, squad briefing, project
-- resource) must keep reading the business column directly and never join
-- against this table.
--
-- Every effective-content change — edit-save, switch to a historical
-- version, or rollback — is the same underlying write: append a new row
-- here and copy its content into the business column in one transaction.
-- Switching/rolling back never rewrites or deletes an existing row, so the
-- version line itself is the audit trail; there is no separate event-log
-- table (see RUYI-179 ADR-001 §4.2).
--
-- No foreign keys by house rule: workspace_id / scope_id / author_user_id
-- integrity is enforced in the application layer, and every write path
-- re-validates its references inside the transaction that uses them.
--
-- The three secondary indexes on this table are inlined at the bottom of
-- this file (RUYI-359 consolidation): they build without CONCURRENTLY because
-- the table is created in this same implicit transaction (914 precedent).
CREATE TABLE prompt_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'project', 'squad', 'agent')),
    scope_id UUID NOT NULL,
    version INT NOT NULL CHECK (version > 0),
    content TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL,

    -- Provenance of this write. 'import' is the one-time v1 baseline
    -- created from pre-existing content; 'edit' is an explicit save;
    -- 'revert' is a copy-forward rollback (source_version records which
    -- version's content was copied); 'auto_snapshot' is reserved for a
    -- future automatic checkpoint, not written by this issue's code.
    source TEXT NOT NULL CHECK (source IN ('import', 'edit', 'revert', 'auto_snapshot')),
    source_version INT,
    change_note TEXT NOT NULL DEFAULT '',

    -- Which scanner revision cleared this content, and what it found. A
    -- pass means "no rule of this revision matched", never "contains no
    -- secret", so the revision travels with the row. Findings are
    -- category / rule / line only — matched text never lands here, in a
    -- log, or in an error body.
    scanner_revision TEXT NOT NULL DEFAULT '',
    gate_result JSONB NOT NULL DEFAULT '[]'::jsonb,

    author_user_id UUID,
    author_note_issue_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE prompt_version IS
    'Version history for the four prompt tiers (RUYI-183). History/audit only — the business column on workspace/project/squad/agent stays the single read source for currently effective content. Append-only: switching or rolling back writes a new row, never mutates or deletes an existing one.';

-- >>> absorbed from 917.up.sql (RUYI-359 consolidation)

-- Run-level prompt version attribution (RUYI-183, self-evolution phase 1).
--
-- Records, at task-claim time, which prompt_version.version number of each
-- injected tier (workspace/project/squad/agent) this run actually executed
-- with. A tier that was not injected for this run is simply absent from the
-- object — never written as version 0 — so "not applicable" and "version
-- zero" stay distinguishable.
--
-- Shape: {"workspace": <int>, "project": <int>, "squad": <int>, "agent": <int>}
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS prompt_versions JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN agent_task_queue.prompt_versions IS
    'Prompt tier version numbers this run was claimed with (RUYI-183). Keys present only for tiers actually injected; absent, not zero, for tiers that were not.';

-- >>> absorbed from 919.up.sql (RUYI-359 consolidation)

-- One row per (scope, scope_id, version): the invariant every version
-- assignment relies on. Version numbers are assigned under a row lock on
-- the owning entity (workspace/project/squad/agent), so a race that still
-- got past that lock fails here instead of creating a duplicate version.
CREATE UNIQUE INDEX idx_prompt_version_scope_version
    ON prompt_version (scope, scope_id, version);

-- >>> absorbed from 920.up.sql (RUYI-359 consolidation)

-- History list and diff-by-version both read "all versions of this scope
-- entity, newest first".
CREATE INDEX idx_prompt_version_scope_created
    ON prompt_version (scope, scope_id, created_at DESC);

-- >>> absorbed from 921.up.sql (RUYI-359 consolidation)

-- Workspace-level listing/audit views filter by workspace before scope.
CREATE INDEX idx_prompt_version_workspace
    ON prompt_version (workspace_id, created_at DESC);

-- >>> absorbed from 922.up.sql (RUYI-359 consolidation)

-- One-time v1 baseline backfill (RUYI-183, self-evolution phase 1).
--
-- Every existing workspace/project/squad/agent row already carries live
-- prompt content in its business column, predating prompt_version. Without
-- a v1 row, "view history" and "diff between two versions" would have
-- nothing to show for content that already exists, and the next edit would
-- have to special-case "no prior version" instead of reverting to v1 like
-- any other version.
--
-- source='import' distinguishes this synthetic baseline from a real edit.
-- scanner_revision/gate_result stay at their table defaults ('' / '[]'):
-- this backfill is a mechanical snapshot of already-live content, not a new
-- write path, so it does not run the credential/structure gate that guards
-- actual save/switch/rollback requests.
--
-- Idempotent: a migration record that ran once and left rows behind must
-- not insert a duplicate v1 if this file is ever re-applied against a
-- database that already has them, so every INSERT is guarded by
-- WHERE NOT EXISTS on (scope, scope_id, version = 1).
INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT w.id, 'workspace', w.id, 1, COALESCE(w.context, ''), encode(digest(COALESCE(w.context, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM workspace w
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'workspace' AND pv.scope_id = w.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT p.workspace_id, 'project', p.id, 1, COALESCE(p.instructions, ''), encode(digest(COALESCE(p.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM project p
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'project' AND pv.scope_id = p.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT s.workspace_id, 'squad', s.id, 1, COALESCE(s.instructions, ''), encode(digest(COALESCE(s.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM squad s
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'squad' AND pv.scope_id = s.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT a.workspace_id, 'agent', a.id, 1, COALESCE(a.instructions, ''), encode(digest(COALESCE(a.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM agent a
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'agent' AND pv.scope_id = a.id AND pv.version = 1
);

-- >>> absorbed from 937.up.sql (RUYI-359 consolidation)

-- v1 baseline gap backfill (RUYI-213).
--
-- The v1 baseline import above was a one-time snapshot: it gave every
-- workspace/project/squad/agent that existed at the time a v1 prompt_version
-- row. Nothing wrote a baseline for entities created afterwards, so every one
-- of them has empty version history — "view history" and "diff" show nothing,
-- and the content the entity launched with is unrecoverable. The create paths
-- now seed v1 themselves; this section closes the window between that import
-- and that fix.
--
-- Idempotent by the same guard the import above used: WHERE NOT EXISTS on
-- (scope, scope_id, version = 1). An entity that already has a v1 row — from
-- 922, from the create path, or from a previous run of this file — is left
-- exactly as it is. No existing row is ever updated or deleted, so a project
-- whose instructions changed since creation keeps the older baseline it
-- already had rather than having live content written over its history.
INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT w.id, 'workspace', w.id, 1, COALESCE(w.context, ''), encode(digest(COALESCE(w.context, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM workspace w
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'workspace' AND pv.scope_id = w.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT p.workspace_id, 'project', p.id, 1, COALESCE(p.instructions, ''), encode(digest(COALESCE(p.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM project p
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'project' AND pv.scope_id = p.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT s.workspace_id, 'squad', s.id, 1, COALESCE(s.instructions, ''), encode(digest(COALESCE(s.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM squad s
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'squad' AND pv.scope_id = s.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT a.workspace_id, 'agent', a.id, 1, COALESCE(a.instructions, ''), encode(digest(COALESCE(a.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM agent a
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'agent' AND pv.scope_id = a.id AND pv.version = 1
);

-- >>> from former migration 925 (RUYI-359 Phase 2R)

-- >>> absorbed from 925.up.sql (RUYI-359 consolidation)

-- Prompt quality rollup (RUYI-184, self-evolution phase 2): the landing
-- structures for the seven quality dimensions D1..D7, materialised per
-- (prompt tier scope, version, UTC day).
--
-- WHY A ROLLUP AT ALL:
--   The dashboard reads "quality per version over the last 30/90 days" for
--   one scope at a time. Computing that on the read path means joining
--   agent_task_queue x task_usage x task_message per request — task_message
--   alone is the largest child table in the database. ADR-001 §4.4 fixes the
--   read path as a single-table scan of this rollup; everything expensive
--   happens once, in the scheduler job.
--
-- WHY THE FACTS ARE NOT COPIED HERE:
--   Only aggregates and pointers land here. discipline_deductions carries
--   {run_id, rule, points, source} and nothing else — never a prompt line,
--   never a tool argument, never message text. Evidence drill-down re-reads
--   task_message by task_id at request time. That keeps prompt bodies and
--   run transcripts out of a table the dashboard scans wholesale, which is
--   the ADR-002 §3 "source A only" constraint expressed in DDL.
--
-- WHY SO MANY NUMERATOR/DENOMINATOR PAIRS:
--   Every dimension here has a "we did not measure this" state that is NOT
--   zero, and a ratio cannot carry it. D4 stores measured/error tool-result
--   counts instead of a rate so a day whose runs all predate the is_error
--   column (migration 924) reads as measured=0 -> "no data", never 0%. D6
--   splits the failed-run count into the part Prompt can be blamed for and
--   the part it cannot (ADR-002 §11 T4), because a single failed_runs column
--   would silently mix a provider outage into a prompt regression. Same for
--   D2 coverage and D7.
--
-- No foreign keys by house rule: workspace_id / scope_id integrity is the
-- application layer's job, and the rollup job re-derives every key from
-- agent_task_queue on each tick.
--
-- Secondary indexes beyond the inline uniqueness constraints are inlined at
-- the bottom of this file (RUYI-359 consolidation): they build without
-- CONCURRENTLY because the tables are created in this same implicit
-- transaction (914 precedent).
CREATE TABLE prompt_quality_daily (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'project', 'squad', 'agent')),
    scope_id UUID NOT NULL,

    -- The prompt version this day's runs were claimed with, read from
    -- agent_task_queue.prompt_versions (added by migration 916). A run whose claim
    -- did not inject this tier contributes to no row here at all.
    version INT NOT NULL CHECK (version > 0),
    day DATE NOT NULL,

    -- Denominator shared by the per-run dimensions: runs that reached a
    -- terminal state on this day with this tier injected at this version.
    finished_runs INT NOT NULL DEFAULT 0,

    -- D1 injection cost. injected_tokens is a property of the version, not
    -- of the day, and is duplicated onto every row so the dashboard can read
    -- cost and outcome from one scan. NULL means the version's content could
    -- not be read (deleted scope) — not "costs nothing".
    injected_tokens BIGINT,
    run_tokens_median BIGINT,

    -- D2 discipline. discipline_covered_runs is the coverage meta-metric the
    -- ADR requires on the dashboard itself: a median computed from 3 of 200
    -- runs is not the same claim as one computed from 180, and the UI has to
    -- be able to say so. NULL median + 0 coverage is "not measured".
    discipline_score_median NUMERIC,
    discipline_covered_runs INT NOT NULL DEFAULT 0,
    discipline_deductions JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- D4 turn failure rate, stored as its two counts. tool_results_measured
    -- counts tool results whose is_error was actually reported; an
    -- unmeasured result is in neither column. measured = 0 is the "no data"
    -- state the dashboard must render blank rather than 0%.
    tool_results_measured BIGINT NOT NULL DEFAULT 0,
    tool_results_error BIGINT NOT NULL DEFAULT 0,

    -- D5 retries. attempt_total is the sum of agent_task_queue.attempt over
    -- the day's runs; retried_runs counts runs whose attempt > 1. Both are
    -- always measurable (the columns are NOT NULL since migration 055), so
    -- there is no third state here.
    attempt_total INT NOT NULL DEFAULT 0,
    retried_runs INT NOT NULL DEFAULT 0,

    -- D6 failure root cause, split per ADR-002 §11 T4. A failed run whose
    -- failure_reason is an environment/provider class lands in
    -- excluded_failed_runs and is absent from failure_reason_counts, so it
    -- can never move the prompt-attributable rate. failure_reason_counts is
    -- {reason: count} over the attributable runs only.
    attributable_failed_runs INT NOT NULL DEFAULT 0,
    excluded_failed_runs INT NOT NULL DEFAULT 0,
    failure_reason_counts JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- D7 first-pass rate, issue-level coarse measure for this phase: an
    -- issue counts as reviewed once it entered in_review, and as first-pass
    -- when it never fell back from in_review to in_progress. Explicitly
    -- low-confidence (ADR-002 §2.7); reviewed_issues = 0 means "no issue of
    -- this version reached review yet", not a 0% pass rate.
    first_pass_issues INT NOT NULL DEFAULT 0,
    reviewed_issues INT NOT NULL DEFAULT 0,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_prompt_quality_daily_key UNIQUE (scope, scope_id, version, day)
);

COMMENT ON TABLE prompt_quality_daily IS
    'Per (prompt scope, version, UTC day) quality rollup for RUYI-184 dimensions D1/D2/D4/D5/D6/D7. Aggregates and pointers only — no prompt text, no transcript text. Every dimension stores counts rather than rates so "not measured" stays distinguishable from zero.';

-- D3 lives in its own table because it is not a daily aggregate and not a
-- single number per version. The Owner's Q17 ruling is a rule-perplexity /
-- ambiguity-risk score produced by scoring the FULL assembled prompt of ONE
-- runtime profile with a model — and the assembled prompt differs per
-- profile (an ordinary member never receives the leader-task sections), so
-- the profiles are scored separately and must never be averaged together.
--
-- A score is attached to (scope, scope_id, version, runtime_profile) and is
-- re-derivable at any time from the version's content; it does not accrue
-- per day and is not affected by how many runs happened.
--
-- evidence carries per-item findings as {dimension, severity, note} produced
-- by the scoring pass over the workspace's OWN prompt. The prompt body is
-- never stored here: the version content is already in prompt_version, and
-- duplicating it into a dashboard-scanned table would widen the disclosure
-- surface for no read benefit.
CREATE TABLE prompt_perplexity_score (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'project', 'squad', 'agent')),
    scope_id UUID NOT NULL,
    version INT NOT NULL CHECK (version > 0),

    -- Which assembled-prompt variant was scored. 'member' is the ordinary
    -- member runtime; 'leader_task' is the leader task runtime, which
    -- receives extra sections. Scored independently, displayed as separate
    -- sub-tabs, never merged.
    runtime_profile TEXT NOT NULL CHECK (runtime_profile IN ('member', 'leader_task')),

    band TEXT NOT NULL CHECK (band IN ('low', 'medium', 'high')),

    -- The declared reproducibility interval for this score, in percent. A
    -- single point estimate would imply a precision repeated scoring does
    -- not have; the band plus this interval is the whole claim.
    percent_low NUMERIC NOT NULL CHECK (percent_low >= 0 AND percent_low <= 100),
    percent_high NUMERIC NOT NULL CHECK (percent_high >= 0 AND percent_high <= 100),
    CONSTRAINT ck_prompt_perplexity_percent_order CHECK (percent_low <= percent_high),

    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Which model produced the score. A band from one model is not
    -- comparable with a band from another, so the dashboard shows it and the
    -- job refuses to average across values.
    model TEXT NOT NULL DEFAULT '',
    scored_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_prompt_perplexity_key UNIQUE (scope, scope_id, version, runtime_profile)
);

COMMENT ON TABLE prompt_perplexity_score IS
    'D3 rule-perplexity / ambiguity-risk score per (prompt scope, version, runtime profile) (RUYI-184, Owner Q17). Scored per runtime profile and never averaged across profiles. Stores band + declared interval + per-item findings; never the prompt body.';

-- Single-row watermark for the rollup job, same shape as
-- task_usage_hourly_rollup_state (migration 101): a SMALLINT(1) primary key
-- is the cheapest way to spell "exactly one row".
--
-- WHY A WATERMARK AND NOT A DIRTY QUEUE:
--   task_usage_hourly needs a dirty queue because its buckets can be
--   re-keyed by an UPDATE (issue.project_id moving usage to a new project)
--   and emptied by a DELETE, neither of which an updated_at watermark can
--   see. This rollup has neither hazard: a run's (scope, version, day) key
--   is fixed at claim time by agent_task_queue.prompt_versions and never
--   moves, and the job recomputes each touched bucket from scratch rather
--   than incrementing it, so a re-scanned bucket converges instead of
--   double-counting. Adding triggers on agent_task_queue — the hottest table
--   in the database — to buy nothing would be the wrong trade.
CREATE TABLE prompt_quality_rollup_state (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    watermark_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    last_run_started_at TIMESTAMPTZ,
    last_run_finished_at TIMESTAMPTZ,
    last_run_rows BIGINT NOT NULL DEFAULT 0,
    last_error TEXT
);
INSERT INTO prompt_quality_rollup_state (id) VALUES (1) ON CONFLICT DO NOTHING;

-- >>> absorbed from 926.up.sql (RUYI-359 consolidation)

-- The dashboard's only read shape: one scope, all versions, last 30/90 days,
-- newest first. The unique constraint from 925 leads with (scope, scope_id,
-- version), which cannot serve a range on day without scanning every version
-- of the scope, so the range column comes second here.
CREATE INDEX IF NOT EXISTS idx_prompt_quality_daily_scope_day
    ON prompt_quality_daily (scope, scope_id, day DESC);

-- >>> absorbed from 927.up.sql (RUYI-359 consolidation)

-- Workspace teardown and cross-scope audit reads. prompt_quality_daily rows
-- are keyed by scope, so deleting a workspace has no other way to find them
-- than a full scan (see the workspace_delete manifest).
CREATE INDEX IF NOT EXISTS idx_prompt_quality_daily_workspace
    ON prompt_quality_daily (workspace_id);

-- >>> absorbed from 928.up.sql (RUYI-359 consolidation)

-- Same reason as 927: the only key on prompt_perplexity_score is the scope
-- tuple, and workspace teardown deletes by workspace_id.
CREATE INDEX IF NOT EXISTS idx_prompt_perplexity_score_workspace
    ON prompt_perplexity_score (workspace_id);

-- >>> from former migration 929 (RUYI-359 Phase 2R)

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

-- >>> from former migration 958 (RUYI-359 Phase 2R)

-- >>> absorbed from 958.up.sql (RUYI-359 consolidation)

-- RUYI-305 E2: the rebuilt proposal pool. A proposal is a Prompt
-- improvement draft (条款草案) targeting one of the four prompt carriers
-- (workspace context / project instructions / squad instructions / agent
-- instructions — the prompt_version scope set), with the clause's final
-- text (not an intent description), the structured content-gate five
-- answers, and evidence anchors pointing at the source issues.
--
-- State machine (RUYI-298 方案 A, dry-run patches 5–8):
--
--   draft --submit(creator)--> pending_owner
--   pending_owner --approve(owner)--> approved --> gate --> enacted | gate_failed
--   pending_owner --reject(owner)--> rejected
--   gate_failed --rework(creator)--> draft
--   rejected --restore(owner)--> draft
--   enacted --(carrier revert hook)--> rolled_back audit marker
--
-- `enacted` is executed by the system after the gate passes — approve runs
-- the gate synchronously; /enact exists only as the owner's recovery path
-- for an `approved` row left by a mid-flight crash. The gate validates the
-- sandbox-synthesized full carrier text (not the fragment) and is
-- fail-closed: any engine error blocks enactment.
--
-- E5 boundary: enacted_version stays NULL in E1–E4. The write into the
-- carrier's effective content and the prompt_version snapshot binding are
-- RUYI-285-thread scope; this column is the interface they will fill.
--
-- No foreign keys by house rule; carrier_scope_id is validated against the
-- scope's owning table on every write path. Secondary indexes are inlined
-- below (RUYI-359 consolidation of the former separate index migration).
CREATE TABLE prompt_proposal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- 拟落点: carrier type + target entity + target section (a `## ` heading
    -- of the carrier text; empty means "no section / document tail").
    carrier_scope TEXT NOT NULL CHECK (carrier_scope IN ('workspace', 'project', 'squad', 'agent')),
    carrier_scope_id UUID NOT NULL,
    target_section TEXT NOT NULL DEFAULT '',

    -- 变更类型: add / revise / remove a named clause. remove must leave no
    -- dangling references — the gate errors if the clause name is still
    -- referenced anywhere in the synthesized text.
    change_kind TEXT NOT NULL CHECK (change_kind IN ('add_clause', 'revise_clause', 'remove_clause')),
    clause_name TEXT NOT NULL,
    clause_text TEXT NOT NULL DEFAULT '',

    -- 内容闸五答 (structured, all required): 层次归属 / 去留判据 / 代价声明 /
    -- 同主题冲突裁决 / 重复检查结论.
    gate_answer_layer TEXT NOT NULL,
    gate_answer_retention TEXT NOT NULL,
    gate_answer_cost TEXT NOT NULL,
    gate_answer_conflict TEXT NOT NULL,
    gate_answer_dedup TEXT NOT NULL,

    -- 证据锚: [{issue_id, comment_id, ref, note}] — where the friction came
    -- from. Retrospective-sourced drafts always carry at least one issue ref.
    evidence_anchors JSONB NOT NULL DEFAULT '[]'::jsonb,

    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'pending_owner', 'approved', 'enacted', 'gate_failed', 'rejected')),
    gate_errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    gate_warnings JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- E5 interface: set by the (future) enacted-write step, never by E1–E4.
    enacted_version INT,

    -- 回滚审计: the carrier revert hook records why/when the enacted clause's
    -- carrier was rolled back; the proposal can be re-proposed afterwards.
    rollback_reason TEXT NOT NULL DEFAULT '',

    -- 同主题合并: when an intake candidate matches an existing live draft,
    -- no new row is created — evidence anchors accumulate on the existing
    -- row and the candidate is recorded here (NULL = standalone row).
    merged_from JSONB NOT NULL DEFAULT '[]'::jsonb,

    source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'retrospective')),
    created_by_type TEXT NOT NULL DEFAULT 'member' CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,

    -- Append-only transition trail: submit / approve / gate result / reject
    -- / restore / merge / rolled_back, each with actor and timestamp.
    audit_log JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE prompt_proposal IS
    'Prompt legislation proposal pool (RUYI-305 E2): clause drafts for the four prompt carriers with the content-gate five answers, the draft→pending_owner→(gate)→enacted/gate_failed/rejected state machine, and owner-only approve/reject. Replaces the RUYI-265 prophecy pool (dropped in 957).';

-- >>> absorbed from 959.up.sql (RUYI-359 consolidation)

CREATE INDEX idx_prompt_proposal_workspace_status
ON prompt_proposal (workspace_id, status, created_at DESC);

-- >>> from former migration 960 (RUYI-359 Phase 2R)

-- >>> absorbed from 960.up.sql (RUYI-359 consolidation)

-- RUYI-305 E4: per-carrier structure baseline for the legislation gate.
--
-- The baseline is the carrier's approved section set and order (the `## `
-- heading sequence) plus the registered clause names. The gate compares the
-- sandbox-synthesized full text against it; every successful enactment
-- rebuilds the baseline from the synthesized text (dry-run patch 6), so the
-- baseline can never drift from what the owners actually legislated.
--
-- Bootstrap: the first gate run for a carrier creates the baseline from the
-- carrier's current effective content inside the same transaction, so an
-- un-governed carrier is grandfathered exactly once and every later change
-- goes through the gate.
--
-- No foreign keys by house rule; carrier_scope_id validated on write. The
-- unique index is inlined below (RUYI-359 consolidation of the former
-- separate index migration).
CREATE TABLE prompt_structure_baseline (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    carrier_scope TEXT NOT NULL CHECK (carrier_scope IN ('workspace', 'project', 'squad', 'agent')),
    carrier_scope_id UUID NOT NULL,
    sections JSONB NOT NULL DEFAULT '[]'::jsonb,
    clauses JSONB NOT NULL DEFAULT '[]'::jsonb,
    content_sha256 TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE prompt_structure_baseline IS
    'Legislation gate structure baseline per carrier (RUYI-305 E4): approved `## ` section set/order + registered clause names; rebuilt from the synthesized full text on every enacted.';

-- >>> absorbed from 961.up.sql (RUYI-359 consolidation)

CREATE UNIQUE INDEX uidx_prompt_structure_baseline_carrier
ON prompt_structure_baseline (carrier_scope, carrier_scope_id);
