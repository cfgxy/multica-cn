-- Explicit per-agent Quiz range. This repository deliberately avoids foreign
-- keys/cascades; workspace and agent teardown delete these rows explicitly.
CREATE TABLE prompt_quiz_agent_scope (
    agent_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    configured_by UUID,
    configured_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE prompt_quiz_agent_scope_item (
    agent_id UUID NOT NULL,
    item_id UUID NOT NULL,
    PRIMARY KEY (agent_id, item_id)
);

CREATE TABLE prompt_quiz_agent_scope_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    actor_id UUID,
    before_explicit BOOLEAN NOT NULL,
    after_explicit BOOLEAN NOT NULL,
    before_item_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    after_item_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE prompt_quiz_agent_scope IS
    'Explicit Quiz item-range marker for an agent; marker without children disables automatic Quiz delivery.';
