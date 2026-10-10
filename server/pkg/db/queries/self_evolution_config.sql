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
