-- RUYI-630: agent-initiated sensitive-operation authorization, phase 1.
--
-- Two coordinated changes in one atomic stem (single-Issue single-stem rule):
--
--  1. decision_requests — the Issue-independent authorization carrier
--     (方案甲, Owner decision 5). One row PER INVOLVED SPACE grouped by
--     request_group_id: the target-space row is actionable (operator tier
--     forced to the target workspace Owner — decision 3B's second
--     confirmation), the origin-space row is a read-only state projection.
--     The origin-space first-step consent happens on the issue_decisions
--     authorization link card (existing surface + capability columns below);
--     only when the request carries no issue reference does the origin row
--     itself become operable (no-issue scenario, 方案甲 兼容条款). Every
--     state transition dual-writes all rows of the group in one transaction.
--
--  2. issue_decisions capability columns — decision_kind separates question
--     cards (existing three-state semantics untouched, default) from
--     authorization cards; tier declarations, dual-button labels, expiry,
--     structured answer source, the authorization-only state face
--     (auth_state, NULL for question cards) and the pending-action /
--     execution-result fields. Existing rows back-fill to the question-card
--     defaults, so the question-card surface is zero-breakage.
--
-- No foreign keys by repo rule; workspace teardown deletes these rows
-- explicitly in DeleteWorkspaceLeafData (workspace_delete.sql, same change).
-- Agents are hard-blocked from answering (handler-level, plus the
-- RequireHumanActor gate on the answer routes); the source of truth for an
-- authorization is the structured row field answer_source written by the
-- server — user-authored text is never parsed to infer it.

CREATE TABLE decision_requests (
    id UUID PRIMARY KEY,
    -- Groups every per-space row of one authorization; transitions dual-write
    -- the whole group.
    request_group_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    -- origin = the requesting agent's space (read-only projection unless the
    -- request has no issue reference, see operable); target = the space whose
    -- Owner owes the second confirmation (decision 3B).
    role TEXT NOT NULL CHECK (role IN ('origin', 'target')),
    -- false (default): read-only projection — answering is refused. true
    -- only for the target row and for an origin row with no issue reference.
    operable BOOLEAN NOT NULL DEFAULT false,
    -- Per-row lifecycle: pending until this row's own step is answered;
    -- group-level terminal states (denied/expired/revoked and the execution
    -- outcomes) propagate to every row in the same transaction.
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'approved', 'denied', 'expired', 'revoked',
        'executed', 'execute_failed')),
    -- Whitelisted action reference (server-side registry; billing/payment
    -- classes never register) and the frozen parameter snapshot the executor
    -- runs. Params are written once at creation and never updated.
    action_type TEXT NOT NULL,
    action_params JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Registry-derived risk tier, server-authoritative: read | write_low.
    risk_tier TEXT NOT NULL CHECK (risk_tier IN ('read', 'write_low')),
    title TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    origin_workspace_id UUID NOT NULL,
    origin_agent_id UUID NOT NULL,
    origin_task_id UUID,
    -- Title-level reference only: the origin issue leaks no body/comments
    -- through this carrier (决策中心详情不进 Issue).
    origin_issue_id UUID,
    origin_issue_title TEXT,
    -- Who may answer the operable row. Default owner = fail-closed. The
    -- target row forces 'owner' at creation (decision 3B).
    operator_tier TEXT NOT NULL DEFAULT 'owner' CHECK (operator_tier IN ('owner', 'named', 'members')),
    named_approver_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Custom dual-button labels; NULL falls back to 同意/拒绝 client-side.
    approve_label TEXT,
    deny_label TEXT,
    -- Platform-capped at creation (default 24h, decision 4). Lazy expiry.
    expires_at TIMESTAMPTZ NOT NULL,
    answered_by_type TEXT CHECK (answered_by_type = 'member'),
    answered_by_id UUID,
    answered_at TIMESTAMPTZ,
    -- Structured source of the answer, written by the server only
    -- (card_click for the button path; text_token/batch reserved for the
    -- question-card surface).
    answer_source TEXT CHECK (answer_source IN ('card_click', 'text_token', 'batch')),
    executed_at TIMESTAMPTZ,
    execution_result JSONB,
    execution_error TEXT,
    -- Set when the terminal-state callback to the origin agent has been
    -- posted once for the group (expiry sweep callbacks fire at most once).
    terminal_callback_at TIMESTAMPTZ,
    -- Creation is agent-initiated by definition (the channel exists because
    -- agent credentials cannot reach the sensitive endpoint); the creator
    -- agent may cancel its own pending request.
    created_by_type TEXT NOT NULL DEFAULT 'agent' CHECK (created_by_type = 'agent'),
    created_by_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Decision-center list per workspace: status is the first filter axis.
CREATE INDEX idx_decision_requests_workspace_status
    ON decision_requests (workspace_id, status, created_at DESC);

-- Group fan-out reads (dual-write sync, terminal propagation).
CREATE INDEX idx_decision_requests_group
    ON decision_requests (request_group_id);

-- "Requests raised by my runs" lookups (agent CLI status checks).
CREATE INDEX idx_decision_requests_origin_agent
    ON decision_requests (origin_agent_id);

-- Lazy-expiry sweep: due groups are found by expires_at, then flipped.
CREATE INDEX idx_decision_requests_expires
    ON decision_requests (expires_at)
    WHERE status IN ('pending', 'approved');

-- ---------------------------------------------------------------------------
-- issue_decisions capability columns (phase-1 architecture item 2). All
-- nullable/defaulted so existing question cards and every existing query
-- keep their exact semantics.
-- ---------------------------------------------------------------------------
ALTER TABLE issue_decisions ADD COLUMN decision_kind TEXT NOT NULL DEFAULT 'question'
    CHECK (decision_kind IN ('question', 'authorization'));
-- Visibility tier: members (default, every workspace member) | restricted
-- (Owner + named approvers only). Machine identities never see either.
ALTER TABLE issue_decisions ADD COLUMN visible_tier TEXT NOT NULL DEFAULT 'members'
    CHECK (visible_tier IN ('members', 'restricted'));
-- Operator tier: owner (default) | named (Owner + named approvers) | members.
-- Declared tiers violating visibility containment converge to owner
-- server-side before insert (fail-closed).
ALTER TABLE issue_decisions ADD COLUMN operator_tier TEXT NOT NULL DEFAULT 'owner'
    CHECK (operator_tier IN ('owner', 'named', 'members'));
ALTER TABLE issue_decisions ADD COLUMN named_approver_ids JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE issue_decisions ADD COLUMN approve_label TEXT;
ALTER TABLE issue_decisions ADD COLUMN deny_label TEXT;
ALTER TABLE issue_decisions ADD COLUMN expires_at TIMESTAMPTZ;
-- Structured answer source, server-written on every answer path; NULL on
-- rows answered before this migration.
ALTER TABLE issue_decisions ADD COLUMN answer_source TEXT
    CHECK (answer_source IN ('card_click', 'text_token', 'batch'));
-- Authorization-only state face; question cards keep NULL forever and their
-- open/answered/cancelled status semantics are untouched.
ALTER TABLE issue_decisions ADD COLUMN auth_state TEXT
    CHECK (auth_state IN ('pending', 'approved', 'denied', 'expired', 'revoked', 'executed', 'execute_failed'));
-- The authorization link card's tie to its decision_requests group and the
-- action snapshot it advertises.
ALTER TABLE issue_decisions ADD COLUMN pending_request_group_id UUID;
ALTER TABLE issue_decisions ADD COLUMN pending_action_type TEXT;
ALTER TABLE issue_decisions ADD COLUMN pending_action_params JSONB;
ALTER TABLE issue_decisions ADD COLUMN execution_result JSONB;
ALTER TABLE issue_decisions ADD COLUMN execution_error TEXT;
ALTER TABLE issue_decisions ADD COLUMN executed_at TIMESTAMPTZ;
