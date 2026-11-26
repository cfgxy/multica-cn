-- Per-agent Quiz bank selection. Presence of a scope row means the selection is
-- explicit; no child rows is therefore a deliberate empty range, not legacy
-- "all items" behavior. Workspaces without a scope row retain the historical
-- all-active-item sweep until an owner configures one.
CREATE TABLE prompt_quiz_agent_scope (
    agent_id UUID PRIMARY KEY REFERENCES agent(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    configured_by UUID,
    configured_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE prompt_quiz_agent_scope_item (
    agent_id UUID NOT NULL REFERENCES prompt_quiz_agent_scope(agent_id) ON DELETE CASCADE,
    item_id UUID NOT NULL REFERENCES prompt_quiz_item(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, item_id)
);

CREATE INDEX prompt_quiz_agent_scope_workspace_idx ON prompt_quiz_agent_scope(workspace_id);

COMMENT ON TABLE prompt_quiz_agent_scope IS
    'Explicit Quiz item-range marker for an agent; a marker with no children disables automatic Quiz delivery for that agent.';
