-- RUYI-359 consolidation: absorbs 959 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 958.up.sql (RUYI-359 consolidation)

-- RUYI-305 E2: the rebuilt proposal pool. A proposal is a Prompt
-- improvement draft (条款草案) targeting one of the four prompt carriers
-- (workspace context / project instructions / squad instructions / agent
-- instructions — the prompt_version scope set), with the clause's final
-- text (not an intent description), the structured content-gate five
-- answers, and evidence anchors pointing at the source issues.
--
-- State machine (RUYI-298 方案 A, dry-run patches 5–8):
--
--   draft --submit(creator)--> pending_owner
--   pending_owner --approve(owner)--> approved --> gate --> enacted | gate_failed
--   pending_owner --reject(owner)--> rejected
--   gate_failed --rework(creator)--> draft
--   rejected --restore(owner)--> draft
--   enacted --(carrier revert hook)--> rolled_back audit marker
--
-- `enacted` is executed by the system after the gate passes — approve runs
-- the gate synchronously; /enact exists only as the owner's recovery path
-- for an `approved` row left by a mid-flight crash. The gate validates the
-- sandbox-synthesized full carrier text (not the fragment) and is
-- fail-closed: any engine error blocks enactment.
--
-- E5 boundary: enacted_version stays NULL in E1–E4. The write into the
-- carrier's effective content and the prompt_version snapshot binding are
-- RUYI-285-thread scope; this column is the interface they will fill.
--
-- No foreign keys by house rule; carrier_scope_id is validated against the
-- scope's owning table on every write path. Secondary indexes are inlined
-- below (RUYI-359 consolidation of the former separate index migration).
CREATE TABLE prompt_proposal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- 拟落点: carrier type + target entity + target section (a `## ` heading
    -- of the carrier text; empty means "no section / document tail").
    carrier_scope TEXT NOT NULL CHECK (carrier_scope IN ('workspace', 'project', 'squad', 'agent')),
    carrier_scope_id UUID NOT NULL,
    target_section TEXT NOT NULL DEFAULT '',

    -- 变更类型: add / revise / remove a named clause. remove must leave no
    -- dangling references — the gate errors if the clause name is still
    -- referenced anywhere in the synthesized text.
    change_kind TEXT NOT NULL CHECK (change_kind IN ('add_clause', 'revise_clause', 'remove_clause')),
    clause_name TEXT NOT NULL,
    clause_text TEXT NOT NULL DEFAULT '',

    -- 内容闸五答 (structured, all required): 层次归属 / 去留判据 / 代价声明 /
    -- 同主题冲突裁决 / 重复检查结论.
    gate_answer_layer TEXT NOT NULL,
    gate_answer_retention TEXT NOT NULL,
    gate_answer_cost TEXT NOT NULL,
    gate_answer_conflict TEXT NOT NULL,
    gate_answer_dedup TEXT NOT NULL,

    -- 证据锚: [{issue_id, comment_id, ref, note}] — where the friction came
    -- from. Retrospective-sourced drafts always carry at least one issue ref.
    evidence_anchors JSONB NOT NULL DEFAULT '[]'::jsonb,

    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'pending_owner', 'approved', 'enacted', 'gate_failed', 'rejected')),
    gate_errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    gate_warnings JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- E5 interface: set by the (future) enacted-write step, never by E1–E4.
    enacted_version INT,

    -- 回滚审计: the carrier revert hook records why/when the enacted clause's
    -- carrier was rolled back; the proposal can be re-proposed afterwards.
    rollback_reason TEXT NOT NULL DEFAULT '',

    -- 同主题合并: when an intake candidate matches an existing live draft,
    -- no new row is created — evidence anchors accumulate on the existing
    -- row and the candidate is recorded here (NULL = standalone row).
    merged_from JSONB NOT NULL DEFAULT '[]'::jsonb,

    source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'retrospective')),
    created_by_type TEXT NOT NULL DEFAULT 'member' CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,

    -- Append-only transition trail: submit / approve / gate result / reject
    -- / restore / merge / rolled_back, each with actor and timestamp.
    audit_log JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE prompt_proposal IS
    'Prompt legislation proposal pool (RUYI-305 E2): clause drafts for the four prompt carriers with the content-gate five answers, the draft→pending_owner→(gate)→enacted/gate_failed/rejected state machine, and owner-only approve/reject. Replaces the RUYI-265 prophecy pool (dropped in 957).';

-- >>> absorbed from 959.up.sql (RUYI-359 consolidation)

CREATE INDEX idx_prompt_proposal_workspace_status
ON prompt_proposal (workspace_id, status, created_at DESC);
