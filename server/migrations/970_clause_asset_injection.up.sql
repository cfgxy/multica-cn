-- RUYI-751 / ADR 007 phase 1: clauses become first-class, versioned,
-- independently injectable assets.
--
-- Today a clause is only a bold-label line inside a carrier's business
-- column: no stable identity (G1), no own version line (G2), no movable
-- scope (G3), no clause-level injection count (G4). These tables give the
-- clause all four, plus the observation surface the dynamic claim-time
-- injection needs:
--
--   clause            identity + lifecycle status (active/deprecated/revoked)
--   clause_version    append-only enacted body snapshots
--   clause_binding    where a clause is in force, with task_kind conditions
--   clause_injection  one row per (task, clause version) actually injected
--   clause_quality_daily  per (clause, version, day) quality rollup
--   clause_quality_rollup_state   the rollup's singleton watermark
--
-- House rules honored here:
--   - No new database foreign keys. workspace_id / clause_id / task_id /
--     scope_id are plain UUIDs; DeleteWorkspace* sweeps clause-family rows in
--     the application layer, and proposal→clause references are validated at
--     write time (same treatment as prompt_structure_baseline, migration 908).
--   - New-table indexes are plain CREATE [UNIQUE] INDEX inside this file: the
--     whole stem is one implicit transaction on empty tables (migration 961
--     precedent), so a concurrent build would only add failure modes. No
--     existing table gains an index in this migration.
--   - Single issue, single stem: all six objects plus the prompt_proposal
--     reference-key columns share this one number.
--
-- The binding's partial unique index (one ACTIVE binding per clause) is the
-- concurrency backstop for move_clause: two proposals racing the same clause
-- across carriers cannot both commit (ADR 007 §1). It is a partial unique
-- INDEX rather than a constraint because only the active row is unique —
-- superseded/withdrawn history stays multi-row by design.

CREATE TABLE clause (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    -- Stable public identity used by the carrier marker contract
    -- (<!-- clause:id=... rev=... -->) and by proposals as their reference
    -- key. Format 'cls_' + 8 lowercase alphanumerics; immutable for life —
    -- the display title below is what may change.
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    -- Lifecycle: active (in force) → deprecated (removed/retired; version
    -- line kept, excluded from the effective set) → revoked (terminal).
    -- draft/proposed/approved are proposal-pool states, never asset states.
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated', 'revoked')),
    created_by_type TEXT NOT NULL DEFAULT 'member' CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug)
);

COMMENT ON TABLE clause IS
    'Governance clause asset (RUYI-751 / ADR 007): stable identity and lifecycle status only. The effective body lives in clause_version, the effective scope in clause_binding — this row is never the read source for injected text.';
COMMENT ON COLUMN clause.slug IS
    'Marker-contract identity (cls_ + 8 lowercase alphanumerics), unique per workspace, immutable for life; rendered into carrier snapshot markers and used by proposals as the reference key.';
COMMENT ON COLUMN clause.title IS
    'Display name (the former clause_name). Free to change; never an identity.';
COMMENT ON COLUMN clause.status IS
    'Lifecycle: active = in force; deprecated = removed/retired, version line kept, excluded from the effective set; revoked = terminal, requires a new proposal to rebuild.';

CREATE TABLE clause_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clause_id UUID NOT NULL,
    version INT NOT NULL CHECK (version > 0),
    content TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    -- Provenance, mirroring prompt_version.source vocabulary: 'legislation'
    -- for proposal enactments, 'snapshot_restore' for restore.sh-driven
    -- re-imports, 'seed' for legacy backfills.
    source TEXT NOT NULL DEFAULT 'legislation',
    -- Carrier the version was enacted into (traceability; the binding, not
    -- this column, decides where the clause is in force today).
    carrier_scope TEXT NOT NULL CHECK (carrier_scope IN ('workspace', 'project', 'squad', 'agent')),
    carrier_version INT,
    created_by_type TEXT NOT NULL DEFAULT 'member' CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (clause_id, version)
);

COMMENT ON TABLE clause_version IS
    'Append-only clause body snapshots (RUYI-751): one row per enacted version. The sole fact source for injected text; never updated in place.';
COMMENT ON COLUMN clause_version.carrier_version IS
    'prompt_version.version of the carrier at enactment time; NULL when the carrier had no governance version yet.';

CREATE TABLE clause_binding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clause_id UUID NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'project', 'squad', 'agent')),
    scope_id UUID NOT NULL,
    -- Effective-relationship state, kept separate from clause.status on
    -- purpose (ADR 007 §1a): asset status records the clause itself,
    -- binding status records the relationship. move_clause supersedes the
    -- old row and inserts the new one; remove_clause withdraws.
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'superseded', 'withdrawn')),
    -- Effectiveness conditions, ADR 007 §1 minimal set:
    -- {"task_kinds": ["issue", "chat", ...]} — NULL (SQL NULL) or a NULL
    -- task_kinds key means "applies to every task kind". More dimensions may
    -- join the object later without a migration; the CHECK only pins the
    -- top-level shape.
    conditions JSONB,
    since_clause_version INT NOT NULL DEFAULT 1,
    audit_log JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE clause_binding IS
    'Where a clause is in force (RUYI-751): one active row per clause (partial unique index below), history kept as superseded/withdrawn rows. Conditions carry the task_kind filter for dynamic injection.';
COMMENT ON COLUMN clause_binding.conditions IS
    'NULL = applies to every task kind; otherwise an object whose optional task_kinds array whitelists task kinds (issue/chat/autopilot/quick_create).';

-- The move_clause race backstop: at most one ACTIVE binding per clause, so
-- two concurrent cross-carrier moves cannot both commit. Application code
-- still serializes on the carrier row locks; this index is what makes the
-- invariant hold even if a future path forgets to.
CREATE UNIQUE INDEX uidx_clause_binding_active_clause
    ON clause_binding (clause_id) WHERE status = 'active';

-- Effective-set point lookup: active bindings for one carrier scope entity.
CREATE INDEX idx_clause_binding_scope_active
    ON clause_binding (scope, scope_id) WHERE status = 'active';

-- Workspace-wide active set (snapshot re-render across all four carriers).
CREATE INDEX idx_clause_binding_workspace_active
    ON clause_binding (clause_id) WHERE status = 'active';

CREATE TABLE clause_injection (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    task_id UUID NOT NULL,
    clause_id UUID NOT NULL,
    clause_version INT NOT NULL,
    -- Carrier whose effective set selected this clause (attribution travels
    -- with the injection, so a clause moved across carriers keeps its
    -- historical rows readable).
    carrier_scope TEXT NOT NULL CHECK (carrier_scope IN ('workspace', 'project', 'squad', 'agent')),
    carrier_version INT,
    -- True when the row was recorded from the carrier snapshot fallback
    -- rather than a successful dynamic computation — the governance view
    -- reads degraded runs from this flag instead of a separate ledger.
    degraded BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Claim redelivery/retry idempotency (prompt_quiz_result precedent):
    -- a task re-claimed after a crash re-writes the same rows harmlessly.
    UNIQUE (task_id, clause_id, clause_version)
);

COMMENT ON TABLE clause_injection IS
    'One row per (task, clause version) actually injected into a run (RUYI-751 / ADR 007 §6): the sole basis for the "call count" metric. Write failures are logged-and-swallowed — attribution is observability, never a claim precondition; the standing reconciliation view covers the gap.';
COMMENT ON COLUMN clause_injection.degraded IS
    'True = recorded from the carrier snapshot fallback (dynamic computation failed or the global kill switch is on), so the row set may be the full unfiltered snapshot rather than the task_kind-filtered effective set.';

CREATE INDEX idx_clause_injection_clause_time
    ON clause_injection (clause_id, clause_version, created_at);

CREATE INDEX idx_clause_injection_workspace_time
    ON clause_injection (workspace_id, created_at);

-- Per (clause, version, UTC day) quality rollup — the clause-level mirror of
-- prompt_quality_daily with the same dimensions (D1/D2/D4/D5/D6/D7) and the
-- same "store counts, never rates; unmeasured ≠ 0" discipline. The run set
-- comes from clause_injection joined to the same per-run facts the prompt
-- rollup reads; the aggregation day is the run's terminal-state day.
CREATE TABLE clause_quality_daily (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    clause_id UUID NOT NULL,
    clause_version INT NOT NULL CHECK (clause_version > 0),
    day DATE NOT NULL,

    finished_runs INT NOT NULL DEFAULT 0,

    injected_tokens BIGINT,
    run_tokens_median BIGINT,

    discipline_score_median NUMERIC,
    discipline_covered_runs INT NOT NULL DEFAULT 0,
    discipline_deductions JSONB NOT NULL DEFAULT '[]'::jsonb,

    tool_results_measured BIGINT NOT NULL DEFAULT 0,
    tool_results_error BIGINT NOT NULL DEFAULT 0,

    attempt_total INT NOT NULL DEFAULT 0,
    retried_runs INT NOT NULL DEFAULT 0,

    attributable_failed_runs INT NOT NULL DEFAULT 0,
    excluded_failed_runs INT NOT NULL DEFAULT 0,
    failure_reason_counts JSONB NOT NULL DEFAULT '{}'::jsonb,

    first_pass_issues INT NOT NULL DEFAULT 0,
    reviewed_issues INT NOT NULL DEFAULT 0,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_clause_quality_daily_key UNIQUE (clause_id, clause_version, day)
);

COMMENT ON TABLE clause_quality_daily IS
    'Per (clause, version, UTC day) quality rollup (RUYI-751): clause-level mirror of prompt_quality_daily. Counts only — rates are derived at read time so "not measured" stays distinguishable from zero.';

CREATE INDEX idx_clause_quality_daily_clause_day
    ON clause_quality_daily (clause_id, day DESC);

CREATE INDEX idx_clause_quality_daily_workspace
    ON clause_quality_daily (workspace_id);

CREATE TABLE clause_quality_rollup_state (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    watermark_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    last_run_started_at TIMESTAMPTZ,
    last_run_finished_at TIMESTAMPTZ,
    last_run_rows BIGINT NOT NULL DEFAULT 0,
    last_error TEXT
);
INSERT INTO clause_quality_rollup_state (id) VALUES (1) ON CONFLICT DO NOTHING;

COMMENT ON TABLE clause_quality_rollup_state IS
    'Singleton watermark for the clause_quality_daily rollup job; same shape and contract as prompt_quality_rollup_state.';

-- ── prompt_proposal: clause reference keys (ADR 007 §8, G1 closure) ────────
-- revise/remove/move proposals reference the clause by clause_id; clause_name
-- stays as the display field only. Legacy name-only rows keep enacting via
-- the name fallback in the handler; new drafts resolve the id at creation.

ALTER TABLE prompt_proposal ADD COLUMN clause_id UUID;
ALTER TABLE prompt_proposal ADD COLUMN move_to_scope TEXT
    CONSTRAINT prompt_proposal_move_to_scope_check
    CHECK (move_to_scope IS NULL OR move_to_scope IN ('workspace', 'project', 'squad', 'agent'));
ALTER TABLE prompt_proposal ADD COLUMN move_to_scope_id UUID;
-- Effectiveness conditions authored with the draft (ADR 007 §1 minimal set:
-- {"task_kinds": [...]}), copied onto the binding at enact. NULL = 全适用.
ALTER TABLE prompt_proposal ADD COLUMN conditions JSONB
    CONSTRAINT prompt_proposal_conditions_shape
    CHECK (conditions IS NULL OR jsonb_typeof(conditions) = 'object');
-- Set when this draft is an auto-generated rollback suggestion (ADR 007 §7):
-- the version the suggestion reverts from; existence of a live row with the
-- same (clause_id, rollback_of_version) suppresses duplicate suggestions.
ALTER TABLE prompt_proposal ADD COLUMN rollback_of_version INT;

-- move_clause joins the state machine. Replacing (not dropping+re-adding
-- blind) keeps the constraint name stable for the down migration.
ALTER TABLE prompt_proposal DROP CONSTRAINT prompt_proposal_change_kind_check;
ALTER TABLE prompt_proposal ADD CONSTRAINT prompt_proposal_change_kind_check
    CHECK (change_kind IN ('add_clause', 'revise_clause', 'remove_clause', 'move_clause'));

-- Auto-generated rollback drafts are their own provenance class: neither
-- manual nor retrospective, and the quality job is the only writer.
ALTER TABLE prompt_proposal DROP CONSTRAINT prompt_proposal_source_check;
ALTER TABLE prompt_proposal ADD CONSTRAINT prompt_proposal_source_check
    CHECK (source IN ('manual', 'retrospective', 'system'));

-- The enactment re-render funnels carrier business columns through
-- prompt_version with source='legislation' (ADR 007 §8): the write is the
-- legislation transaction's render snapshot, not a user edit, and the
-- ledger must say so. Keeps the 921 vocabulary intact — 'snapshot' is the
-- prompt-governance snapshot source and must survive this rewrite.
ALTER TABLE prompt_version DROP CONSTRAINT prompt_version_source_check;
ALTER TABLE prompt_version ADD CONSTRAINT prompt_version_source_check
    CHECK (source IN ('import', 'edit', 'revert', 'auto_snapshot', 'snapshot', 'legislation'));

COMMENT ON COLUMN prompt_proposal.clause_id IS
    'RUYI-751: the referenced clause for revise/remove/move proposals; clause_name is display-only from here on. NULL on legacy name-only rows (resolved at enact via the name fallback).';
COMMENT ON COLUMN prompt_proposal.move_to_scope IS
    'RUYI-751 move_clause: destination carrier scope. NULL for every other change kind.';
COMMENT ON COLUMN prompt_proposal.move_to_scope_id IS
    'RUYI-751 move_clause: destination entity id within the workspace. NULL for every other change kind.';
COMMENT ON COLUMN prompt_proposal.rollback_of_version IS
    'RUYI-751: set on auto-generated rollback suggestion drafts — the clause version the suggestion reverts from; live rows with the same (clause_id, rollback_of_version) suppress duplicate suggestions.';
COMMENT ON COLUMN prompt_proposal.conditions IS
    'RUYI-751: draft-level effectiveness conditions ({"task_kinds": [...]}, NULL = 全适用), copied onto the clause_binding row at enact. Shape-checked at the DB, value-checked in the handler (task kind whitelist).';
