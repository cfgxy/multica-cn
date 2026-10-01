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
