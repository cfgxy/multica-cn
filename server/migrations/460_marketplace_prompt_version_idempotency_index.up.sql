-- Idempotency-Key is scoped to its publisher: the same key from two users is
-- two distinct requests, and a global key space would let one user's retry
-- collide with another's first attempt.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_marketplace_prompt_version_idempotency
    ON marketplace_prompt_version (publisher_user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
