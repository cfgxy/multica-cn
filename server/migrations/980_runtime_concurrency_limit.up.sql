ALTER TABLE agent_runtime
    ADD COLUMN max_concurrent_tasks INT NOT NULL DEFAULT 0
    CHECK (max_concurrent_tasks >= 0);
