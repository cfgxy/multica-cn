-- RUYI-551: per-workspace model-service configuration for the self-evolution
-- module. One row per workspace drives every LLM consumer the module owns
-- (quality scoring and the daily retrospective) through the priority chain:
-- module config in this table > deploy-injected default (MULTICA_LLM_*) >
-- unconfigured. The row is optional: a workspace that never saves the card
-- keeps riding the deploy default.
--
-- api_key_encrypted holds secretbox AES-256-GCM ciphertext of the gateway API
-- key — the same at-rest construction as runtime_credential.payload_encrypted
-- (house rule: secrets are never stored as plaintext columns). No API ever
-- reads it back to the client: validation runs server-side and only the
-- outcome below is persisted.
--
-- A row may exist with NULL credentials (the scoring switch flipped without a
-- gateway override); resolvers treat NULL base_url/model as "no override" and
-- keep consuming the deploy default while honoring scoring_enabled.
CREATE TABLE self_evolution_model_config (
    workspace_id UUID PRIMARY KEY,
    base_url TEXT,
    api_key_encrypted BYTEA,
    model TEXT,
    -- 质量评分开关 (module config card): false turns the D1–D4 scoring
    -- pipeline off for this workspace regardless of which source resolves.
    scoring_enabled BOOLEAN NOT NULL DEFAULT true,
    last_validated_at TIMESTAMPTZ,
    last_validation_ok BOOLEAN,
    last_validation_error TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE self_evolution_model_config IS
    'Self-evolution module model-service config per workspace (RUYI-551): gateway base_url/model, secretbox-encrypted API key, scoring switch, last validation outcome. Module config outranks the deploy default.';
