-- --- Self-evolution model-service config (RUYI-551) ---
-- One row per workspace drives the module's LLM consumers through the
-- priority chain: this table > deploy default > unconfigured. The API key
-- column only ever carries secretbox ciphertext (see migration 934); no
-- query returns it to a client.

-- name: GetSelfEvolutionModelConfig :one
SELECT * FROM self_evolution_model_config WHERE self_evolution_model_config.workspace_id = @workspace_id;

-- name: UpsertSelfEvolutionModelConfig :one
INSERT INTO self_evolution_model_config (
    workspace_id, base_url, api_key_encrypted, model, scoring_enabled,
    last_validated_at, last_validation_ok, last_validation_error, updated_at
) VALUES (
    @workspace_id, @base_url, @api_key_encrypted, @model, @scoring_enabled,
    @last_validated_at, @last_validation_ok, @last_validation_error, now()
)
ON CONFLICT (workspace_id) DO UPDATE SET
    base_url = EXCLUDED.base_url,
    api_key_encrypted = EXCLUDED.api_key_encrypted,
    model = EXCLUDED.model,
    scoring_enabled = EXCLUDED.scoring_enabled,
    last_validated_at = EXCLUDED.last_validated_at,
    last_validation_ok = EXCLUDED.last_validation_ok,
    last_validation_error = EXCLUDED.last_validation_error,
    updated_at = now()
RETURNING *;

-- name: UpdateSelfEvolutionModelConfigValidation :exec
-- Records the outcome of a server-side validation pass (save-time mandatory
-- or re-validate from the status card). Never stores request payloads.
UPDATE self_evolution_model_config SET
    last_validated_at = @last_validated_at,
    last_validation_ok = @last_validation_ok,
    last_validation_error = @last_validation_error,
    updated_at = now()
WHERE self_evolution_model_config.workspace_id = @workspace_id;

-- name: DeleteSelfEvolutionModelConfig :exec
-- 恢复部署默认: the whole row goes, credentials and validation state with it.
DELETE FROM self_evolution_model_config WHERE self_evolution_model_config.workspace_id = @workspace_id;

-- --- Workspace JEV advisory config ---
-- This endpoint is a warn-only prompt-proposal sidecar, not a model-service
-- override. Its key remains encrypted and is never selected into an API
-- response outside the server-side resolver.

-- name: GetSelfEvolutionJevConfig :one
SELECT * FROM self_evolution_jev_config WHERE self_evolution_jev_config.workspace_id = @workspace_id;

-- name: UpsertSelfEvolutionJevConfig :one
INSERT INTO self_evolution_jev_config (
    workspace_id, base_url, api_key_encrypted, model, timeout_ms,
    last_checked_at, last_check_ok, last_check_error, last_engine, updated_at
) VALUES (
    @workspace_id, @base_url, @api_key_encrypted, @model, @timeout_ms,
    @last_checked_at, @last_check_ok, @last_check_error, @last_engine, now()
)
ON CONFLICT (workspace_id) DO UPDATE SET
    base_url = EXCLUDED.base_url,
    api_key_encrypted = EXCLUDED.api_key_encrypted,
    model = EXCLUDED.model,
    timeout_ms = EXCLUDED.timeout_ms,
    last_checked_at = EXCLUDED.last_checked_at,
    last_check_ok = EXCLUDED.last_check_ok,
    last_check_error = EXCLUDED.last_check_error,
    last_engine = EXCLUDED.last_engine,
    updated_at = now()
RETURNING *;

-- name: UpdateSelfEvolutionJevConfigCheck :exec
UPDATE self_evolution_jev_config SET
    last_checked_at = @last_checked_at,
    last_check_ok = @last_check_ok,
    last_check_error = @last_check_error,
    last_engine = @last_engine,
    updated_at = now()
WHERE self_evolution_jev_config.workspace_id = @workspace_id;

-- name: DeleteSelfEvolutionJevConfig :exec
DELETE FROM self_evolution_jev_config WHERE self_evolution_jev_config.workspace_id = @workspace_id;
