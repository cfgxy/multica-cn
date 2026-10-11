-- Reverse of 942_drop_agent_webhook: recreate the table exactly as
-- migration 900_agent_webhooks defined it (DDL copied verbatim, historical
-- FK shape included) so a down applies cleanly on databases that ran 900.
CREATE TABLE agent_webhook (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) <= 50),
    prompt TEXT NOT NULL CHECK (char_length(prompt) <= 4000),
    token TEXT NOT NULL UNIQUE,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_webhook_agent ON agent_webhook(agent_id, created_at DESC);
