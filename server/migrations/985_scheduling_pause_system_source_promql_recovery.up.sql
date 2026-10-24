-- RUYI-859 forward recovery for databases that recorded the stage-A 982
-- migration before the PromQL rule source fields were added to its file.
--
-- Migration ledgers record the complete stem, not the file contents. Editing
-- 982 therefore cannot repair an already-applied database. This same-number
-- follow-up stem keeps RUYI-859's allocated 985 migration identity while
-- bringing both the historical and clean-install shapes to the PromQL
-- scheduling contract. There are no foreign keys or secondary indexes.

-- A recorded historical 130 can likewise have a missing permission_mode after
-- an interrupted/manual schema repair. 130 handles the inverse collision
-- (column present before its ledger row) with IF NOT EXISTS; this forward
-- recovery handles the ledger-recorded shape without rewriting its history.
ALTER TABLE agent
    ADD COLUMN IF NOT EXISTS permission_mode TEXT NOT NULL DEFAULT 'private'
        CHECK (permission_mode IN ('private', 'public_to'));

ALTER TABLE agent_backpressure_policy
    ADD COLUMN IF NOT EXISTS promql_rule_ids UUID[] NOT NULL DEFAULT '{}';

ALTER TABLE agent_suppression_event
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'error_rate',
    ADD COLUMN IF NOT EXISTS rule_id UUID,
    ADD COLUMN IF NOT EXISTS rule_name TEXT,
    ADD COLUMN IF NOT EXISTS reason TEXT;

CREATE TABLE IF NOT EXISTS agent_promql_rule_pause (
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    rule_id UUID NOT NULL,
    rule_name TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT 'rule_firing',
    entered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, agent_id, rule_id)
);

COMMENT ON COLUMN agent_backpressure_policy.promql_rule_ids IS
    'Stable IDs of selected workspace PromQL rules that may independently hold this agent behind the PromQL claim fence. Validated by the handler against the locked workspace settings; no foreign key by design.';

COMMENT ON TABLE agent_promql_rule_pause IS
    'One independently-owned PromQL-rule scheduling pause per selected agent. ClaimAgentTask consults this table alongside scheduling_pause; deleting one row can never release a human, error-rate, or another rule source.';
