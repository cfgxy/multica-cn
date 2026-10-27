-- Renumbered from the former migration 965 by the RUYI-359 Phase 2R
-- domain consolidation (gap-free 900+ ladder); content otherwise unchanged.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 965.up.sql (RUYI-359 consolidation)

-- RUYI-304: durable run-trigger intent ledger for channel ordinary messages.
--
-- A row is upserted inside the message-append transaction (BeforeCommit), so
-- "this context generation has an untriggered agent run" becomes a database
-- fact the moment the message lands. The in-process debounce batcher stays as
-- the EARLY trigger only; a hard crash inside the debounce window can no longer
-- drop the trigger — channel_chat_run_reconciler claims due 'pending' rows and
-- runs the same EnqueueChannelChatTask the flush would have run.
--
-- state machine: 'pending' (run not yet enqueued) → 'fired' (the enqueue
-- transaction CAS-cleared it) or 'dead' (terminal: session archived, agent row
-- deleted, route superseded by a newer generation, or retries exhausted).
--
-- Trigger snapshot mirrors the debounced flush arguments exactly; the row and
-- the logs never carry message bodies or credentials.
CREATE TABLE channel_chat_run_intent (
    id                UUID NOT NULL,
    workspace_id      UUID NOT NULL,
    chat_session_id   UUID NOT NULL,
    context_revision  BIGINT NOT NULL,
    -- Window-visible trigger snapshot: last sender wins (MUL-2645).
    initiator_user_id UUID,
    force_fresh       BOOLEAN NOT NULL DEFAULT FALSE,
    -- Delivery-route fence snapshot at last upsert; the reconciler enqueues
    -- with the same expected binding/route the flush would pass.
    binding_id        UUID NOT NULL,
    route_revision    BIGINT NOT NULL,
    -- Ops diagnostics / terminal-notice routing; no logic keys on it today.
    installation_id   UUID,
    state             TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'fired', 'dead')),
    -- Compensation deadline: upsert time + 2x the debounce window. The batcher
    -- flush normally wins the race; the reconciler only sees rows whose flush
    -- genuinely never ran (crash, wedged worker).
    fire_at           TIMESTAMPTZ NOT NULL,
    attempts          INT NOT NULL DEFAULT 0,
    next_attempt_at   TIMESTAMPTZ,
    claimed_by        UUID,
    claim_expires_at  TIMESTAMPTZ,
    dead_reason       TEXT,
    last_error        TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- >>> absorbed from 966.up.sql (RUYI-359 consolidation)

-- RUYI-304: unique index backing channel_chat_run_intent's primary key
-- (attached below via USING INDEX). Originally its own single-statement
-- migration so CONCURRENTLY ran outside an implicit transaction block (repo
-- convention, see migrations 228 and 208); inlined by RUYI-359 because the
-- table is created in this same implicit transaction.
CREATE UNIQUE INDEX channel_chat_run_intent_id_uidx
    ON channel_chat_run_intent (id);

-- >>> absorbed from 967.up.sql (RUYI-359 consolidation)

-- RUYI-304: attach the primary key via the unique index above
-- (same three-step pattern as migration 229; inlined by RUYI-359).
ALTER TABLE channel_chat_run_intent
    ADD CONSTRAINT channel_chat_run_intent_pkey PRIMARY KEY USING INDEX channel_chat_run_intent_id_uidx;

-- >>> absorbed from 968.up.sql (RUYI-359 consolidation)

-- RUYI-304: at most ONE 'pending' intent per (chat_session, context_revision).
-- This is the upsert conflict target: every message in a debounce window
-- extends the same row (later fire_at, latest sender); once the row leaves
-- 'pending' the next message on the same generation legitimately inserts a
-- fresh pending row for the next window.
CREATE UNIQUE INDEX channel_chat_run_intent_pending_uidx
    ON channel_chat_run_intent (chat_session_id, context_revision)
    WHERE state = 'pending';

-- >>> absorbed from 969.up.sql (RUYI-359 consolidation)

-- RUYI-304: serves the reconciler's due-row scan (state + compensation
-- deadline). Originally its own single-statement migration for CONCURRENTLY
-- (repo convention, see migration 230); inlined by RUYI-359.
CREATE INDEX IF NOT EXISTS idx_channel_chat_run_intent_claim
    ON channel_chat_run_intent (state, fire_at);
