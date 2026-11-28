-- RUYI-971: Multica Issue structured AC and dynamic TODO execution ledger.
CREATE TABLE IF NOT EXISTS issue_acceptance_criteria (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    key TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'uncompleted' CHECK (status IN ('uncompleted', 'pending_verification', 'verified', 'rejected')),
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    rejection_reason TEXT NOT NULL DEFAULT '',
    verified_at TIMESTAMPTZ,
    verified_by_type TEXT,
    verified_by_id UUID,
    position INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS issue_todos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'pending_verification', 'completed', 'blocked')),
    next_step TEXT NOT NULL DEFAULT '',
    blocked_reason TEXT NOT NULL DEFAULT '',
    assignee_type TEXT CHECK (assignee_type IN ('member', 'agent')),
    assignee_id UUID,
    ac_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    position INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS issue_task_state_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    target_type TEXT NOT NULL CHECK (target_type IN ('ac', 'todo')),
    target_id UUID NOT NULL,
    action TEXT NOT NULL,
    from_status TEXT NOT NULL DEFAULT '',
    to_status TEXT NOT NULL DEFAULT '',
    actor_type TEXT NOT NULL,
    actor_id UUID NOT NULL,
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
