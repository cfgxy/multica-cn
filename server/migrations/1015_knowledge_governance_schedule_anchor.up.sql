-- Anchor existing configuration versions to their first future *local*
-- schedule boundary. A migration must not turn an already-enabled workspace into
-- an immediate mid-window run merely because the anchor column was introduced.
ALTER TABLE knowledge_governance_config
    ADD COLUMN next_eligible_at TIMESTAMPTZ;

UPDATE knowledge_governance_config
SET next_eligible_at = CASE cadence
    WHEN 'daily' THEN (
        date_trunc('day', now() AT TIME ZONE timezone) + INTERVAL '1 day'
    ) AT TIME ZONE timezone
    WHEN 'weekly' THEN (
        date_trunc('week', now() AT TIME ZONE timezone) + INTERVAL '1 week'
    ) AT TIME ZONE timezone
    WHEN 'monthly' THEN (
        date_trunc('month', now() AT TIME ZONE timezone) + INTERVAL '1 month'
    ) AT TIME ZONE timezone
END;

ALTER TABLE knowledge_governance_config
    ALTER COLUMN next_eligible_at SET NOT NULL;

COMMENT ON COLUMN knowledge_governance_config.next_eligible_at IS
    'First schedule window at which this configuration version may be claimed; written with the configuration audit transaction.';
