-- Renumbered from the former migration 943 by the RUYI-359 Phase 2R
-- domain consolidation (gap-free 900+ ladder); content otherwise unchanged.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 943.up.sql (RUYI-359 consolidation)

-- Self-evolution daily proposals (RUYI-265, phase-4 spec §A, B1–B3).
-- A proposal is dead weight until it carries a falsifiable prophecy (B1):
-- the prophecy JSONB is fixed at creation and no endpoint rewrites it.
-- Adoption (the Owner decision) and verification (whether the prophecy held)
-- are recorded in separate columns (B2) so neither can masquerade as the
-- other. Rejected proposals keep their row and prophecy for retrieval but
-- never gain a prompt/skill version link, so they structurally cannot enter
-- the quality curve (B3) — the curve is driven only by version rows.
CREATE TABLE proposal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    -- Spec §A.2 type matrix; the required prophecy shape depends on it:
    -- prompt_revision/skill carry a quantitative band, the knowledge types
    -- carry an observable behavior change plus a falsification condition.
    type TEXT NOT NULL CHECK (type IN ('prompt_revision', 'project_cognition', 'lesson', 'pitfall', 'skill')),
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'needs_revision', 'adopted', 'rejected', 'archived')),
    title TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    -- Free-form evidence entries (issue refs, links, earlier proposal ids —
    -- rejected proposals are valid evidence inputs per §A.5).
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- B1 prophecy, frozen at creation: object (what is predicted), direction,
    -- outcome_text, absolute band (range_low/range_high) for quantitative
    -- types, falsify_condition for behavior types.
    prophecy JSONB NOT NULL,
    -- Two snapshots (§A.2): generation_snapshot is captured when the proposal
    -- is created (reference only); adoption_snapshot is captured inside the
    -- adoption action and is the audit baseline for verification. Neither
    -- ever rewrites the prophecy itself.
    generation_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    adoption_snapshot JSONB,
    -- B2: verification outcome(s), stored apart from the adoption decision.
    -- Manual marks append to a history array instead of overwriting.
    verification JSONB,
    -- Append-only state-transition trail (reject reasons, restores, transfer
    -- failures) — the retention record B3 retrieval renders.
    audit_log JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Knowledge-type adoption transfer (spec §A.3 r4): adoption and the
    -- transfer into the ultimate knowledge base are one server flow; on
    -- failure the proposal stays un-adopted with the reason here.
    transfer_error TEXT NOT NULL DEFAULT '',
    created_by_type TEXT NOT NULL DEFAULT 'member' CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE proposal IS 'Self-evolution proposals: falsifiable prophecy fixed at creation (B1), adoption and verification recorded separately (B2), rejected proposals retained without version links (B3).';

-- >>> absorbed from 944.up.sql (RUYI-359 consolidation)

CREATE INDEX idx_proposal_workspace_status
ON proposal (workspace_id, status);

-- >>> absorbed from 955.up.sql (RUYI-359 consolidation)

-- RUYI-289: every newly discovered knowledge source whose first scan finds
-- entries generates exactly one system proposal into the pool. The dir id is
-- recorded inside generation_snapshot; this partial unique index makes the
-- "one proposal per directory, ever" rule database-enforced, so a daemon
-- re-report or a server retry cannot mint a second proposal for the same
-- source. Human/member proposals never carry the key and are unaffected.
CREATE UNIQUE INDEX uidx_proposal_system_dir
ON proposal ((generation_snapshot->>'knowledge_dir_id'))
WHERE generation_snapshot ? 'knowledge_dir_id';
