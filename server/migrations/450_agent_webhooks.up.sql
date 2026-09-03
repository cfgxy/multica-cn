-- Agent webhooks (RUYI-52): public, token-bearing trigger URLs bound to a
-- fixed prompt. Each visit to /api/webhooks/agents/{token} creates a fresh
-- chat session on the owning agent whose first user message is the bound
-- prompt. The token is a bearer credential ("awt_" prefix, 256-bit entropy)
-- generated in handler/agent_webhook.go; the UNIQUE constraint lets the
-- public ingress resolve it in O(1).
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
