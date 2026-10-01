-- RUYI-305 E3: the daily retrospective — a base-layer scheduled task that
-- analyzes the real execution content of issues completed inside the window
-- (descriptions, comments, PR links, status transitions — the B-semantics
-- correction: no pre-generated retrospective comments are read), distills
-- Prompt improvement drafts and drops them into the proposal pool.
--
-- It never writes to issues: no comments, no reports, no notifications.
-- Failures are visible only on the self-evolution retrospective page
-- (retrospective_run) — dry-run patches 1–4 and 9–10.
--
-- retrospective_config is per-workspace, owner-writable. retrospective_run
-- is the run record the page renders. retrospective_issue_watermark is the
-- per-issue idempotency水位 (dry-run patch 2): an issue analyzed by a run is
-- never analyzed again, so overlapping windows cannot produce duplicate
-- drafts. PKs inline by convention; secondary indexes in 956/957.
CREATE TABLE retrospective_config (
    workspace_id UUID PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT false,
    -- 已完成口径: default done only; include_in_review widens the scan to
    -- issues sitting in in_review as well (dry-run patch 1).
    include_in_review BOOLEAN NOT NULL DEFAULT false,
    -- 分析时间窗口 in days (1–30): how far back a run looks for newly
    -- completed issues. The watermark bounds actual re-analysis.
    window_days INT NOT NULL DEFAULT 1 CHECK (window_days BETWEEN 1 AND 30),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE retrospective_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed')),
    trigger TEXT NOT NULL DEFAULT 'schedule' CHECK (trigger IN ('schedule', 'manual')),
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    issues_scanned INT NOT NULL DEFAULT 0,
    issues_analyzed INT NOT NULL DEFAULT 0,
    proposals_created INT NOT NULL DEFAULT 0,
    proposals_merged INT NOT NULL DEFAULT 0,
    duplicates_skipped INT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

CREATE TABLE retrospective_issue_watermark (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    last_run_id UUID NOT NULL,
    analyzed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE retrospective_config IS
    'Daily retrospective config per workspace (RUYI-305 E3): enabled flag, done/in_review scan scope, window days. Owner-writable.';
COMMENT ON TABLE retrospective_run IS
    'Retrospective run records (RUYI-305 E3): window, counts, error. The only surface retrospective failures ever appear on.';
COMMENT ON TABLE retrospective_issue_watermark IS
    'Per-issue retrospective idempotency watermark (RUYI-305 E3): an analyzed issue is never analyzed again, window overlap cannot duplicate drafts.';
