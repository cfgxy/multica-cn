-- RUYI-400: durable capability-probe state for channel bot installations.
--
-- After a Feishu installation succeeds, the backend probes each runtime
-- capability (send / read history / media download / contact lookup) with a
-- synthetic-id API call and records the honest tri-state here. The web UI
-- reads these rows to render the 缺权补授权 panel instead of forcing the
-- operator to rediscover scope gaps from chat failures.
--
-- Why a dedicated table and not a config blob: channel_installation.config
-- is hashed verbatim by rowFingerprint — any write there flips the fingerprint
-- and restarts the WS connection. Probe results are read-only diagnostics and
-- must not flap the link. (No FK by house rule; the workspace sweep reaches
-- rows through ws_installations before the installation delete.)
--
-- required_scopes freezes the catalog snapshot at check time, so the UI can
-- name the exact scopes to add even after the catalog evolves.
-- checked_at is the probe wall clock; stale rows simply read as "unknown yet".
CREATE TABLE channel_capability_state (
    id              UUID NOT NULL,
    installation_id UUID NOT NULL,
    -- Deliberately generic: the probe machinery is channel-agnostic and a
    -- DingTalk/other adapter may reuse it without a schema change.
    channel_type    TEXT NOT NULL,
    capability      TEXT NOT NULL,
    status          TEXT NOT NULL
        CHECK (status IN ('granted', 'missing', 'unknown')),
    -- Sanitized verdict detail (business code + classification only — probes
    -- read no tenant content, so nothing here can leak message bodies).
    detail          TEXT NOT NULL DEFAULT '',
    required_scopes JSONB NOT NULL DEFAULT '[]',
    checked_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Primary key via the unique-index three-step (same pattern as migration
-- 915): the migrator sends each file as a single implicit transaction, so
-- these indexes build non-concurrently alongside the table they depend on.
CREATE UNIQUE INDEX channel_capability_state_id_uidx
    ON channel_capability_state (id);

ALTER TABLE channel_capability_state
    ADD CONSTRAINT channel_capability_state_pkey PRIMARY KEY USING INDEX channel_capability_state_id_uidx;

-- At most one probe-state row per (installation, channel, capability) —
-- the upsert conflict target.
CREATE UNIQUE INDEX channel_capability_state_uidx
    ON channel_capability_state (installation_id, channel_type, capability);
