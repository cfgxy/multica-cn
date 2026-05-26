-- Lark (飞书) Bot integration: per-agent PersonalAgent installations,
-- user/chat bindings, inbound dedup + drop audit, outbound card mapping,
-- and short-lived member binding tokens.
--
-- Scope notes (mirror description §4.8 boundaries):
--   * `chat_session` is reused as-is — Lark routes through a separate
--     `lark_chat_session_binding` rather than adding a `metadata` JSONB
--     column to chat_session.
--   * Outbound card-message mapping is *task/message* scoped, not session
--     scoped, so multiple runs on the same chat_session don't stomp each
--     other's cards.
--   * `app_secret` is stored encrypted; the application layer encrypts
--     before writing and decrypts on read (no DB-side decryption helper).
--   * `lark_inbound_audit` is the only writable surface for events that
--     fail identity check or group-mention filter — it stores routing /
--     identity / drop_reason / timestamp ONLY, never message body.

-- =====================
-- lark_installation
-- =====================
-- One row per (workspace, agent) — each Multica Agent owns at most one
-- Lark PersonalAgent Bot. `app_secret_encrypted` is the ciphertext
-- produced by the application-layer secretbox helper; never plaintext.
CREATE TABLE lark_installation (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id          UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id              UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    app_id                TEXT NOT NULL,
    -- Ciphertext of the Lark app secret. Application-layer secretbox.
    -- DB never sees plaintext; a dump leaks ciphertext only.
    app_secret_encrypted  BYTEA NOT NULL,
    tenant_key            TEXT,
    bot_open_id           TEXT NOT NULL,
    installer_user_id     UUID NOT NULL REFERENCES "user"(id) ON DELETE RESTRICT,
    status                TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'revoked')),
    -- WS ownership lease: only the server instance holding a non-expired
    -- lease may keep the WebSocket open for this installation. Used to
    -- prevent duplicate consumption when multiple replicas are deployed.
    ws_lease_token        TEXT,
    ws_lease_expires_at   TIMESTAMPTZ,
    installed_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, agent_id),
    UNIQUE (app_id),
    -- Composite key target for the composite FK on lark_user_binding
    -- (installation_id, workspace_id) — guarantees a binding's workspace
    -- always matches the workspace of its installation.
    UNIQUE (id, workspace_id)
);

CREATE INDEX idx_lark_installation_workspace ON lark_installation(workspace_id);
CREATE INDEX idx_lark_installation_agent ON lark_installation(agent_id);
-- Used by the lease scanner to find leases due for renewal / takeover.
CREATE INDEX idx_lark_installation_lease ON lark_installation(ws_lease_expires_at)
    WHERE status = 'active';

-- =====================
-- lark_user_binding
-- =====================
-- Maps a Lark `open_id` to a Multica user, per-installation. open_id is
-- per-app, so the same Lark user has different open_ids across different
-- installations — the binding is therefore keyed on (installation, open_id),
-- not open_id alone. `union_id` is captured opportunistically for future
-- cross-installation identity merging (Phase 2) but is not authoritative
-- in MVP.
--
-- Two structural invariants protect §4.3's "unbound or non-workspace
-- members never leak content into chat_session" rule from drifting if
-- the application layer regresses:
--
--   1. The composite FK on (installation_id, workspace_id) targets
--      lark_installation(id, workspace_id), so a binding row cannot
--      claim a workspace different from its installation's workspace.
--
--   2. The composite FK on (workspace_id, multica_user_id) targets
--      member(workspace_id, user_id) with ON DELETE CASCADE, so when a
--      Multica user is removed from the workspace the stale Lark
--      binding is removed in the same transaction. There is no path
--      where lark_user_binding can outlive workspace membership.
CREATE TABLE lark_user_binding (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID NOT NULL,
    multica_user_id  UUID NOT NULL,
    installation_id  UUID NOT NULL,
    lark_open_id     TEXT NOT NULL,
    union_id         TEXT,
    bound_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (installation_id, lark_open_id),
    -- Installation ↔ workspace integrity. Composite FK guarantees the
    -- binding's workspace_id matches the installation's workspace_id.
    CONSTRAINT lark_user_binding_installation_fk
        FOREIGN KEY (installation_id, workspace_id)
        REFERENCES lark_installation(id, workspace_id)
        ON DELETE CASCADE,
    -- Workspace membership integrity. Composite FK guarantees the
    -- (workspace_id, multica_user_id) pair still exists in member; when
    -- the user is removed from the workspace, the binding cascades away.
    CONSTRAINT lark_user_binding_member_fk
        FOREIGN KEY (workspace_id, multica_user_id)
        REFERENCES member(workspace_id, user_id)
        ON DELETE CASCADE
);

CREATE INDEX idx_lark_user_binding_user
    ON lark_user_binding(multica_user_id, workspace_id);
CREATE INDEX idx_lark_user_binding_workspace_open
    ON lark_user_binding(workspace_id, lark_open_id);

-- =====================
-- lark_chat_session_binding
-- =====================
-- One Lark chat (`chat_id`) ↔ one Multica `chat_session`. The Lark side
-- doesn't distinguish p2p vs group at the routing layer, but we keep
-- `lark_chat_type` for product behavior (group sessions only ingest
-- @-Bot / reply-Bot messages; p2p ingests everything).
CREATE TABLE lark_chat_session_binding (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_session_id   UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    installation_id   UUID NOT NULL REFERENCES lark_installation(id) ON DELETE CASCADE,
    lark_chat_id      TEXT NOT NULL,
    lark_chat_type    TEXT NOT NULL
        CHECK (lark_chat_type IN ('p2p', 'group')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (installation_id, lark_chat_id),
    UNIQUE (chat_session_id)
);

CREATE INDEX idx_lark_chat_session_binding_session
    ON lark_chat_session_binding(chat_session_id);

-- =====================
-- lark_inbound_message_dedup
-- =====================
-- Idempotency for Lark inbound events. WebSocket reconnects can replay
-- recently-delivered events; we keep 24h of message_ids to short-circuit
-- replays before any business logic runs. A periodic vacuum job (separate
-- migration / cron) trims rows older than ~24h.
CREATE TABLE lark_inbound_message_dedup (
    message_id    TEXT PRIMARY KEY,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_lark_inbound_dedup_received
    ON lark_inbound_message_dedup(received_at);

-- =====================
-- lark_inbound_audit
-- =====================
-- Non-content audit log for events that DID arrive but were intentionally
-- dropped (group message without @, unbound user, non-workspace member,
-- duplicate, etc.). NEVER stores message body — only routing + identity
-- + drop_reason + timestamp. Used for ops debugging, abuse detection, and
-- proving the "non-bound users cannot leak content into chat_session"
-- invariant from §4.7 of the design.
CREATE TABLE lark_inbound_audit (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id   UUID REFERENCES lark_installation(id) ON DELETE SET NULL,
    lark_chat_id      TEXT,
    event_type        TEXT NOT NULL,
    lark_event_id     TEXT,
    lark_message_id   TEXT,
    -- Open-ended TEXT (not an enum) so new drop reasons can be added in
    -- application code without a schema migration. Convention: snake_case.
    -- Known values today: unbound_user, non_workspace_member,
    -- not_addressed_in_group, duplicate, revoked_installation, invalid_event.
    drop_reason       TEXT NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_lark_inbound_audit_installation
    ON lark_inbound_audit(installation_id, received_at DESC);
CREATE INDEX idx_lark_inbound_audit_reason
    ON lark_inbound_audit(drop_reason, received_at DESC);

-- =====================
-- lark_outbound_card_message
-- =====================
-- Maps a Multica task (or session bootstrap "thinking…" card) to the
-- Lark interactive card message we're patching. Per-task, not per-session
-- — a chat_session can host many runs and a session-level field would
-- create card aliasing bugs. `task_id` may be NULL for the initial
-- bootstrap card before a task is created; the partial UNIQUE index
-- guarantees task↔card is 1:1 once a task exists.
CREATE TABLE lark_outbound_card_message (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_session_id        UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    task_id                UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    lark_chat_id           TEXT NOT NULL,
    lark_card_message_id   TEXT NOT NULL,
    status                 TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'streaming', 'final', 'error')),
    last_patched_at        TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_lark_outbound_card_task
    ON lark_outbound_card_message(task_id)
    WHERE task_id IS NOT NULL;
CREATE INDEX idx_lark_outbound_card_session
    ON lark_outbound_card_message(chat_session_id, created_at DESC);

-- =====================
-- lark_binding_token
-- =====================
-- Short-lived (≤ 15 min), single-use token for the "you're not bound yet,
-- click here" flow that links a Lark `open_id` to a Multica user. The
-- hash (not the raw token) is stored so a DB leak doesn't grant binding
-- capability. Replay is blocked by `consumed_at IS NOT NULL`.
CREATE TABLE lark_binding_token (
    token_hash       TEXT PRIMARY KEY,
    workspace_id     UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    installation_id  UUID NOT NULL REFERENCES lark_installation(id) ON DELETE CASCADE,
    lark_open_id     TEXT NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    consumed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Belt-and-braces with the application-layer cap (lark.BindingTokenTTL
    -- = 15 minutes). The CHECK refuses any row whose lifetime exceeds the
    -- product cap, so a misconfigured caller or a hand-inserted SQL row
    -- cannot quietly mint a longer-lived binding token. Keep this value
    -- in sync with lark.BindingTokenTTL.
    CONSTRAINT lark_binding_token_ttl_cap
        CHECK (expires_at <= created_at + INTERVAL '15 minutes')
);

CREATE INDEX idx_lark_binding_token_installation
    ON lark_binding_token(installation_id, expires_at);
