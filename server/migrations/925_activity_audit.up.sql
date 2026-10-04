-- RUYI-355 Phase 1: unified Activity/Audit capability.
--
-- One append-only audit_event table as the single source of truth for
-- cross-domain operational events (issue / run / agent / runtime / ops), plus
-- server-side cancel attribution columns on agent_task_queue. Design approved
-- in RUYI-355 (comments 01a0ff41 / 01a0ff4a): "task cancelled by server" must
-- be answerable from queryable evidence — reason, actor, trigger chain — not
-- from process logs.
--
-- The table is append-only by contract: no UPDATE / DELETE path ships with
-- Phase 1. Retention/partitioning is deferred to Phase 2 (design leaves room;
-- the table stays unpartitioned until scale demands otherwise).

CREATE TABLE audit_event (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    -- domain enum: issue | run | agent | runtime | ops
    domain TEXT NOT NULL,
    -- "<domain>.<action>" naming, e.g. run.cancelled, runtime.offline_detected
    event_type TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- actor_type enum: member | agent | system | daemon
    actor_type TEXT NOT NULL,
    -- NULL only for system events (per the write contract)
    actor_id UUID,
    -- causal upstream: type + object reference, semantics reused from
    -- agent_task_queue.trigger_evidence_kind / trigger_evidence_ref_id
    -- (migrations 061 / 184 precedents). No explicit parent pointer: causal
    -- chains are rebuilt from shared object dimensions + trigger + time.
    trigger_kind TEXT,
    trigger_ref TEXT,
    issue_id UUID,
    task_id UUID,
    agent_id UUID,
    runtime_id UUID,
    -- Structured cause. Cancel/failure events carry an enum reason:
    -- issue_deleted | issue_cancelled | reassignment_cleanup |
    -- trigger_comment_deleted | superseded_by_retry | agent_stopped |
    -- agent_archived | user_requested | server_repair | chat_session_deleted |
    -- workspace_teardown | runtime_offline_converged | escalation_acknowledged
    reason TEXT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- Primary query: workspace + time window (newest first). Keyset paging uses
-- (occurred_at, id) as the cursor pair.
CREATE INDEX idx_audit_event_workspace_time ON audit_event (workspace_id, occurred_at DESC, id DESC);

-- Object-dimension lookups ("pull all events for one run / issue / agent /
-- runtime"). New empty table — plain CREATE INDEX is safe here; the
-- CONCURRENTLY rule applies to index builds on existing populated tables.
CREATE INDEX idx_audit_event_issue ON audit_event (issue_id) WHERE issue_id IS NOT NULL;
CREATE INDEX idx_audit_event_task ON audit_event (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_audit_event_agent ON audit_event (agent_id) WHERE agent_id IS NOT NULL;
CREATE INDEX idx_audit_event_runtime ON audit_event (runtime_id) WHERE runtime_id IS NOT NULL;

-- domain/event_type filtering is the second axis of every audit search; a
-- plain btree on (workspace_id, event_type) keeps type-scoped windows cheap.
CREATE INDEX idx_audit_event_workspace_type ON audit_event (workspace_id, event_type);

-- Guard rails: the domain/event_type/actor_type vocabularies are open sets at
-- the SQL level (CHECK would turn every Phase 2 event addition into a
-- migration); the write contract in server/internal/service/audit.go is the
-- enforcement point and is unit-tested. event_type must stay domain-prefixed.
ALTER TABLE audit_event ADD CONSTRAINT audit_event_event_type_prefix_check
    CHECK (event_type = domain || '.' || split_part(event_type, '.', 2));

-- RUYI-355 Phase 1: server-side cancel attribution on the run ledger.
-- Batch cancel paths keep writing status='cancelled' + completed_at; these
-- three columns add WHY and WHO for every server-side path (the user two-phase
-- path already has cancel_requested_by_user_id / cancel_requested_at from
-- migration 916). Written by the same UPDATE that flips the status — same
-- transaction by construction, no second round-trip.
ALTER TABLE agent_task_queue ADD COLUMN cancel_reason TEXT;
ALTER TABLE agent_task_queue ADD COLUMN cancel_actor_type TEXT;
ALTER TABLE agent_task_queue ADD COLUMN cancel_actor_id UUID;
