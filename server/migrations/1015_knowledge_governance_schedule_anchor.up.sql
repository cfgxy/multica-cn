-- Anchor each configuration version to its first future local schedule window.
-- A freshly saved configuration must never backfill the window already in
-- progress; the scheduler consults this same value before it may claim work.
ALTER TABLE knowledge_governance_config
    ADD COLUMN next_eligible_at TIMESTAMPTZ NOT NULL DEFAULT now();

COMMENT ON COLUMN knowledge_governance_config.next_eligible_at IS
    'First schedule window at which this configuration version may be claimed; written with the configuration audit transaction.';
