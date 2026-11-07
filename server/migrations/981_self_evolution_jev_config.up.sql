-- A workspace-owned JEV advisory endpoint. This is deliberately
-- separate from self_evolution_model_config: JEV has a different protocol,
-- failure model and consumer boundary. The API key is secretbox ciphertext;
-- no response or advisory record exposes it.
CREATE TABLE self_evolution_jev_config (
    workspace_id UUID PRIMARY KEY,
    base_url TEXT NOT NULL,
    api_key_encrypted BYTEA,
    model TEXT NOT NULL,
    timeout_ms INTEGER NOT NULL DEFAULT 1500,
    last_checked_at TIMESTAMPTZ,
    last_check_ok BOOLEAN,
    last_check_error TEXT NOT NULL DEFAULT '',
    last_engine TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (timeout_ms >= 100 AND timeout_ms <= 30000)
);

COMMENT ON TABLE self_evolution_jev_config IS
    'Per-workspace JEV warn-only advisory config: endpoint, secretbox-encrypted API key, configured model and last safe connectivity result.';
