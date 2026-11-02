CREATE TABLE knowledge_governance_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    dir_id UUID NOT NULL,
    task_id UUID,
    agent_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled')),
    title TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'retry', 'schedule')),
    detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    report JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

CREATE TABLE knowledge_governance_finding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    entry_id UUID NOT NULL,
    entry_key TEXT NOT NULL,
    original_content TEXT NOT NULL,
    content_version INT NOT NULL,
    content_sha256 TEXT NOT NULL,
    scope TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('valid', 'revised', 'deprecated', 'superseded', 'duplicate', 'invalid', 'needs_review')),
    problem_categories JSONB NOT NULL DEFAULT '[]'::jsonb,
    counter_evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    applicable_scope TEXT NOT NULL,
    replacement_entry_id UUID,
    recommendation TEXT NOT NULL,
    writeback_authorized BOOLEAN NOT NULL DEFAULT false,
    lifecycle_event_ref TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE knowledge_governance_run IS
    'Evidence-led knowledge governance execution records. A run is advisory only; lifecycle writeback remains an explicit owner action.';
COMMENT ON TABLE knowledge_governance_finding IS
    'Immutable per-entry governance findings with snapshot content, counter-evidence, scope, and disposition recommendation.';
