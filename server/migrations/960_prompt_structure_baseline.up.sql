-- RUYI-359 consolidation: absorbs 961 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 960.up.sql (RUYI-359 consolidation)

-- RUYI-305 E4: per-carrier structure baseline for the legislation gate.
--
-- The baseline is the carrier's approved section set and order (the `## `
-- heading sequence) plus the registered clause names. The gate compares the
-- sandbox-synthesized full text against it; every successful enactment
-- rebuilds the baseline from the synthesized text (dry-run patch 6), so the
-- baseline can never drift from what the owners actually legislated.
--
-- Bootstrap: the first gate run for a carrier creates the baseline from the
-- carrier's current effective content inside the same transaction, so an
-- un-governed carrier is grandfathered exactly once and every later change
-- goes through the gate.
--
-- No foreign keys by house rule; carrier_scope_id validated on write. The
-- unique index is inlined below (RUYI-359 consolidation of the former
-- separate index migration).
CREATE TABLE prompt_structure_baseline (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    carrier_scope TEXT NOT NULL CHECK (carrier_scope IN ('workspace', 'project', 'squad', 'agent')),
    carrier_scope_id UUID NOT NULL,
    sections JSONB NOT NULL DEFAULT '[]'::jsonb,
    clauses JSONB NOT NULL DEFAULT '[]'::jsonb,
    content_sha256 TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE prompt_structure_baseline IS
    'Legislation gate structure baseline per carrier (RUYI-305 E4): approved `## ` section set/order + registered clause names; rebuilt from the synthesized full text on every enacted.';

-- >>> absorbed from 961.up.sql (RUYI-359 consolidation)

CREATE UNIQUE INDEX uidx_prompt_structure_baseline_carrier
ON prompt_structure_baseline (carrier_scope, carrier_scope_id);
