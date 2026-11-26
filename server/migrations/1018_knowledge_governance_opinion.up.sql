CREATE TABLE knowledge_governance_opinion (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    dir_id uuid NOT NULL,
    entry_id uuid NOT NULL,
    run_id uuid NOT NULL,
    finding_id uuid NOT NULL,
    action text NOT NULL CHECK (action IN ('revise', 'delete')),
    proposed_content text NOT NULL DEFAULT '',
    target_version integer NOT NULL,
    target_content_sha256 text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'applying', 'applied', 'stale', 'failed', 'unknown')),
    decided_by uuid,
    decided_at timestamptz,
    lifecycle_event_id uuid,
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
