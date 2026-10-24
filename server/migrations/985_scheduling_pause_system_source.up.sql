-- RUYI-859 stage B: automatic error-rate suppression needs a SYSTEM source
-- on scheduling_pause that no human pause can ever hold or lift by mistake.
--
-- Stage A (982) introduced agent_backpressure_policy / agent_suppression_event
-- and left the claim gate and scheduling_pause untouched, so a saved armed
-- policy was inert. Stage B drives it: a server-side loop inserts an agent
-- pause row with created_by IS NULL and the fixed reason
-- 'error_rate_suppression' (the "system source"), and releases it by deleting
-- exactly that row. The human management paths (RUYI-608) always stamp a
-- member user_id, so source ownership is total: a human freeze is never
-- acquired, shared or lifted by the loop, and the loop's own row is never
-- deleted by a human resume (which matches on (workspace, agent), but the
-- human DELETE statement is intentionally left unchanged and is only ever
-- issued by the management surface — the loop uses its own precise-source
-- delete in agent_backpressure.sql).
--
-- The single schema change: created_by becomes nullable. Existing rows all
-- carry a member id (NOT NULL has held since 939_scheduling_pause), so the
-- DROP NOT NULL is a pure widening — no backfill, no behavior change for any
-- reader: the claim fence and the state reader treat created_by as a display
-- attribution, never as a NULL-check. The partial unique indexes are
-- untouched: one agent-level row per (workspace, agent) still makes the
-- ensure idempotent at the storage layer.

ALTER TABLE scheduling_pause
    ALTER COLUMN created_by DROP NOT NULL;

COMMENT ON COLUMN scheduling_pause.created_by IS
    'member.user_id of the human who froze scheduling; NULL marks the system error-rate suppression source (created_by IS NULL together with reason = ''error_rate_suppression'' is the exact system row the automatic loop owns and releases — no human path ever writes NULL).';
