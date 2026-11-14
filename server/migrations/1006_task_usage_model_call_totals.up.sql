-- Keep a task/provider/model aggregate for durable provider call evidence.
-- Prometheus lifetime counters read this task-level table instead of scanning one
-- row per provider call on every refresh. The evidence table remains the source
-- of truth for identity, windowed admission, and completeness decisions.
CREATE TABLE IF NOT EXISTS task_usage_model_call_totals (
    task_id UUID NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    model_call_count BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Rebuild the derived table so a crash after SQL execution but before the
-- migration ledger write can be retried without duplicating backfill rows.
DELETE FROM task_usage_model_call_totals;

-- Recover aggregates for evidence written before this summary table existed.
INSERT INTO task_usage_model_call_totals (task_id, provider, model, model_call_count)
SELECT task_id, provider, model, COUNT(*)::bigint
FROM task_usage_model_call_evidence
GROUP BY task_id, provider, model;
