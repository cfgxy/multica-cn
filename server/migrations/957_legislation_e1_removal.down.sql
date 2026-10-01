-- Down for 957: best-effort structural restore; the dropped `proposal`
-- rows (dev-stage prophecy experiments) are not recoverable.
CREATE TABLE IF NOT EXISTS proposal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('prompt_revision', 'project_cognition', 'lesson', 'pitfall', 'skill')),
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'needs_revision', 'adopted', 'rejected', 'archived')),
    title TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    prophecy JSONB NOT NULL,
    generation_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    adoption_snapshot JSONB,
    verification JSONB,
    audit_log JSONB NOT NULL DEFAULT '[]'::jsonb,
    transfer_error TEXT NOT NULL DEFAULT '',
    created_by_type TEXT NOT NULL DEFAULT 'member' CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE skill_version DROP CONSTRAINT IF EXISTS skill_version_source_check;
ALTER TABLE skill_version ADD CONSTRAINT skill_version_source_check
    CHECK (source IN ('create', 'proposal', 'revision', 'edit', 'restore'));
ALTER TABLE skill_version ADD COLUMN IF NOT EXISTS source_proposal_id UUID;
