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
-- Indexes other than the inline uniqueness constraints are not created here:
-- every CREATE INDEX must be CONCURRENTLY, which cannot run inside a
-- multi-statement file — see 926 / 927.
CREATE TABLE prompt_quality_daily (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'project', 'squad', 'agent')),
    scope_id UUID NOT NULL,

    -- The prompt version this day's runs were claimed with, read from
    -- agent_task_queue.prompt_versions (migration 917). A run whose claim
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
