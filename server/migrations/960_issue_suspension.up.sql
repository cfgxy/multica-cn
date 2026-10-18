-- RUYI-690: issue suspension — "waiting on something" as a dimension orthogonal
-- to the lifecycle status.
--
-- A suspended issue keeps its lifecycle status untouched (a board column never
-- changes because an issue started waiting); the suspension row carries WHY it
-- waits (reason), WHAT it waits on (waiting_on_*), WHEN work may continue
-- (resume_condition for humans, resume_predicate for the machine), and WHO
-- suspended/resumed it (both directions audited). History is the table: every
-- suspension is one row, never an in-place overwrite, so "what did this issue
-- wait for" is a plain SELECT.
--
-- Machine predicates are deliberately a closed set: `issue_done` (the waiting
-- target's effective status entered the done category) and `decision_answered`
-- (the decision card left `open` via answer). `manual` is the catch-all for
-- waiting_on_type external/manual and NEVER auto-resumes — an external
-- condition is judged by a human, and a wrong predicate must never wake work
-- nobody asked for.
--
-- issue.suspended_at is the read-side snapshot (same pattern as
-- run_suppressed/run_suppressed_at, RUYI-275): the board, list and detail
-- views badge "suspended" off this column with zero joins into this table,
-- and it is maintained in the same transaction as the suspension row's
-- status flip. The table remains the audit trail; the column is only a
-- pointer to "an active row exists".
--
-- No foreign keys, per repo rule: workspace_id / issue_id / waiting_on_* /
-- suspended_by / resumed_by reference their tables in the application layer
-- only. DeleteWorkspace sweeps the rows explicitly (workspace.sql CTE);
-- DeleteIssue sweeps the issue's rows the same way.
--
-- The partial unique index makes a double suspend a storage-level no-op: at
-- most one active suspension per issue. It is inlined in this batch rather
-- than built CONCURRENTLY — the table is brand new inside this one implicit
-- transaction, so no concurrent reader can observe an unconstrained build
-- window (same rationale as 925/929/939). The ALTER TABLE on issue is a
-- metadata-only column add with a NULL default; no index is built on it (the
-- aggregate view reads the table, not the column).
CREATE TABLE IF NOT EXISTS issue_suspension (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    -- active → resumed (condition met / manually released) | cancelled
    -- (suspension withdrawn as mistaken or obsolete). Terminal rows are
    -- history; the partial unique index below keeps exactly one active.
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'resumed', 'cancelled')),
    -- Why the issue is waiting, in the author's words. Shown on the badge,
    -- the aggregate view and the detail banner.
    reason TEXT NOT NULL DEFAULT '',
    -- What the issue waits on. `issue` / `decision` rows carry the matching
    -- waiting_on_*_id and a machine-checkable resume_predicate; `external` /
    -- `manual` never auto-resume (predicate kind `manual`).
    waiting_on_type TEXT NOT NULL
        CHECK (waiting_on_type IN ('issue', 'decision', 'external', 'manual')),
    waiting_on_issue_id UUID,
    waiting_on_decision_id UUID,
    -- Human-facing summary of the target when the id alone is not enough
    -- (e.g. "RUYI-668 子单" or a short external description). Derived by the
    -- handler for issue/decision targets, free text otherwise.
    waiting_on_summary TEXT NOT NULL DEFAULT '',
    -- The resume condition in plain language, shown wherever the suspension
    -- is displayed ("RUYI-668 进入 done 后自动恢复").
    resume_condition TEXT NOT NULL DEFAULT '',
    -- Machine predicate for auto-resume. NULL means "no automatic check".
    -- Shapes (validated in the application layer):
    --   {"kind":"issue_done","issue_id":"<uuid>"}
    --   {"kind":"decision_answered","decision_id":"<uuid>"}
    --   {"kind":"manual"}
    resume_predicate JSONB,
    -- Suspension audit: who suspended, when.
    suspended_by_type TEXT NOT NULL,
    suspended_by_id UUID NOT NULL,
    suspended_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Resume audit: who released it (a member/agent via the API, or the
    -- system when a machine predicate fired), when, and the free-text note.
    resumed_by_type TEXT,
    resumed_by_id UUID,
    resumed_at TIMESTAMPTZ,
    resume_note TEXT NOT NULL DEFAULT '',
    -- Withdrawal audit for the cancelled arm (same fields as resume).
    cancelled_by_type TEXT,
    cancelled_by_id UUID,
    cancelled_at TIMESTAMPTZ,
    cancel_note TEXT NOT NULL DEFAULT '',
    -- The predicate that fired for a system resume, denormalized here so the
    -- audit trail shows WHICH condition released the row without re-reading
    -- JSONB. NULL on manual resume/cancel.
    resumed_by_predicate JSONB,
    CHECK (
        (waiting_on_type = 'issue' AND waiting_on_issue_id IS NOT NULL AND waiting_on_decision_id IS NULL)
        OR (waiting_on_type = 'decision' AND waiting_on_decision_id IS NOT NULL AND waiting_on_issue_id IS NULL)
        OR (waiting_on_type IN ('external', 'manual') AND waiting_on_issue_id IS NULL AND waiting_on_decision_id IS NULL)
    )
);

COMMENT ON TABLE issue_suspension IS
    'Issue suspensions (RUYI-690): "waiting" as a dimension orthogonal to the lifecycle status. One row per suspension; the partial unique index keeps at most one active per issue. issue.suspended_at mirrors activeness for join-free board/list badge rendering. No FKs by house rule; swept by workspace/issue deletion in the application layer.';
COMMENT ON COLUMN issue_suspension.resume_predicate IS
    'Closed machine-predicate set: {"kind":"issue_done","issue_id":…} | {"kind":"decision_answered","decision_id":…} | {"kind":"manual"}. Only the first two ever auto-resume; external/manual waits are judged by humans.';
COMMENT ON COLUMN issue_suspension.resumed_by_predicate IS
    'Denormalized copy of the predicate that fired for a system resume, so the audit trail names the releasing condition without a JSONB re-read. NULL on manual resume/cancel.';

CREATE UNIQUE INDEX IF NOT EXISTS uidx_issue_suspension_active_per_issue
    ON issue_suspension (issue_id) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_issue_suspension_workspace_status
    ON issue_suspension (workspace_id, status, suspended_at DESC);

ALTER TABLE issue ADD COLUMN suspended_at timestamptz;

COMMENT ON COLUMN issue.suspended_at IS
    'Read-side snapshot of an active issue_suspension row (RUYI-690), maintained in the same transaction that flips the row''s status. NULL = not suspended. Badge/list/filter surfaces read this column with zero joins; the suspension table owns the audit trail.';
