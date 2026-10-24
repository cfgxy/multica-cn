-- RUYI-858: persist the owner's enactment decision per proposal.
--
-- The approve step (pending_owner -> approved) records WHERE the clause
-- lands: the structured clause library (default — clause asset rows plus
-- the re-rendered snapshot) or the carrier's static prompt text only
-- (the gate-synthesized text lands in the business column + prompt_version
-- + structure baseline, no clause asset rows, no dynamic injection).
--
-- The value is written inside the approval transaction so a crash
-- followed by the /enact recovery path enacts the SAME decision the owner
-- made. move_clause / migrate_clause are constrained to the clause
-- library by the approval handler (fail-closed); their asset write is the
-- whole point of the move.
ALTER TABLE prompt_proposal ADD COLUMN enactment_target TEXT NOT NULL DEFAULT 'clause_library'
    CHECK (enactment_target IN ('clause_library', 'static_prompt'));
