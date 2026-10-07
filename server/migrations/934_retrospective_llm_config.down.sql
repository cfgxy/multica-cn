ALTER TABLE retrospective_config
    DROP COLUMN IF EXISTS llm_api_key_hint,
    DROP COLUMN IF EXISTS llm_api_key_encrypted,
    DROP COLUMN IF EXISTS llm_model,
    DROP COLUMN IF EXISTS llm_base_url;
