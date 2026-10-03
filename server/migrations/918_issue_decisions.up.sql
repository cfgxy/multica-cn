-- Renumbered per Owner instruction (RUYI-345): the table and its companion
-- index previously shipped as two migrations and now land as one atomic
-- migration. A database that applied the earlier pair rewrites its ledger
-- identity rows to this stem in place, preserving applied_at — no DDL is
-- re-executed (rewrite rules: server/cmd/migrate/9xx-consolidation.md).

-- RUYI-345: issue decision cards (Phase 1). An agent (or member) raises a
-- structured question with 2-4 options during a run; a human member answers
-- by picking options; the platform echoes the answer as a comment that
-- mentions the creating agent, reusing the mention pipeline to resume the
-- run. No foreign keys by repo rule; workspace teardown deletes these rows
-- explicitly in DeleteWorkspaceLeafData.
CREATE TABLE issue_decisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    source_comment_id UUID,
    question TEXT NOT NULL,
    options JSONB NOT NULL,
    multi_select BOOLEAN NOT NULL DEFAULT false,
    recommended_indices JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'answered', 'cancelled')),
    selected_indices JSONB,
    answered_by_type TEXT,
    answered_by_id UUID,
    answered_at TIMESTAMPTZ,
    answer_comment_id UUID,
    created_by_type TEXT NOT NULL,
    created_by_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- >>> index statement absorbed from the former single-statement concurrent
-- index migration (RUYI-345 renumber): inlined because the table is created
-- in this same implicit transaction — a build either lands valid or rolls
-- back whole, so no INVALID-leftover cleanup can ever be needed.
CREATE INDEX IF NOT EXISTS idx_issue_decisions_issue
    ON issue_decisions (issue_id, created_at);
