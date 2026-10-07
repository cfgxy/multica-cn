-- RUYI-552: the daily retrospective's LLM configuration becomes a product
-- config owned by the workspace, not a deployment env var. The self-evolution
-- UI saves it here; "run now" and the scheduled job resolve it first and fall
-- back to the MULTICA_LLM_* deployment defaults per field.
--
-- llm_api_key_encrypted is the workspace's LLM API key sealed with the
-- server-side secret box (MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY, the same
-- AES-256-GCM store behind runtime_credential) — never plaintext, never
-- echoed back: the API exposes only llm_api_key_hint (last 4 characters).
-- NULL means "no workspace key; fall back to the deployment default".
-- llm_base_url / llm_model use '' (not NULL) for the same meaning so the
-- per-field fallback is a plain empty check.
ALTER TABLE retrospective_config
    ADD COLUMN llm_base_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN llm_model TEXT NOT NULL DEFAULT '',
    ADD COLUMN llm_api_key_encrypted BYTEA,
    ADD COLUMN llm_api_key_hint TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN retrospective_config.llm_base_url IS
    'Workspace LLM base URL saved from the self-evolution UI (RUYI-552); '''' = fall back to MULTICA_LLM_BASE_URL.';
COMMENT ON COLUMN retrospective_config.llm_model IS
    'Workspace LLM model saved from the self-evolution UI (RUYI-552); '''' = fall back to MULTICA_LLM_DEFAULT_MODEL.';
COMMENT ON COLUMN retrospective_config.llm_api_key_encrypted IS
    'Workspace LLM API key, AES-256-GCM sealed with the server secret box (RUYI-552); NULL = fall back to MULTICA_LLM_API_KEY. Never leaves the server.';
COMMENT ON COLUMN retrospective_config.llm_api_key_hint IS
    'Last 4 characters of the workspace LLM API key for masked display (RUYI-552); the only key surface the API ever returns.';
