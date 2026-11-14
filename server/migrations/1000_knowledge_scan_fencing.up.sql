ALTER TABLE knowledge_dir
    ADD COLUMN IF NOT EXISTS scan_token UUID;
