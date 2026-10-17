-- Reverse 971: restore the pre-migration change_kind vocabulary. Fails with
-- a CHECK violation if any migrate_clause proposal row still exists — the
-- data must be cleaned up first (fail-closed, matching the down-migration
-- discipline for vocabulary widening).
ALTER TABLE prompt_proposal DROP CONSTRAINT prompt_proposal_change_kind_check;
ALTER TABLE prompt_proposal ADD CONSTRAINT prompt_proposal_change_kind_check
    CHECK (change_kind IN ('add_clause', 'revise_clause', 'remove_clause', 'move_clause'));
