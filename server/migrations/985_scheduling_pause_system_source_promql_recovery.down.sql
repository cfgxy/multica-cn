DROP TABLE IF EXISTS agent_promql_rule_pause;

-- permission_mode belongs to historical migration 130. The recovery may have
-- ensured it exists, but must never remove a column owned by that migration.

ALTER TABLE agent_suppression_event
    DROP COLUMN IF EXISTS reason,
    DROP COLUMN IF EXISTS rule_name,
    DROP COLUMN IF EXISTS rule_id,
    DROP COLUMN IF EXISTS source;

ALTER TABLE agent_backpressure_policy
    DROP COLUMN IF EXISTS promql_rule_ids;
