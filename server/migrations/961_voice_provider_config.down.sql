-- Reverse 961_voice_provider_config (RUYI-693). Structural reversal only:
-- the seeded pool rows, their copied runtime_credential secrets and the
-- backfilled agent overrides are not rewound — the up migration is additive
-- and forward-reading (读向兼容，不依赖 down). Down exists so a pre-961
-- schema can be reconstructed on disposable databases; on shared databases
-- migrate down is forbidden by policy.
DELETE FROM runtime_credential
WHERE runtime_instance_id IN (SELECT id FROM voice_provider);

DROP TABLE voice_provider;

ALTER TABLE agent
    DROP COLUMN voice_provider_id,
    DROP COLUMN voice_model,
    DROP COLUMN voice_advanced;
