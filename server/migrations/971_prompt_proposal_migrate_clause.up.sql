-- RUYI-764 / ADR 007 拆单 5: legacy migration needs a change kind that
-- expresses "adopt an existing rule block as a clause asset". migrate_clause
-- reuses the add_clause asset-write path, but its synthesize step locates the
-- block in the carrier by label and requires it to match the inventory text
-- byte-for-byte — so the 733-audit drift check is re-enforced inside the
-- enact gate (fail-closed). Same vocabulary-widening shape as 970's
-- move_clause.
ALTER TABLE prompt_proposal DROP CONSTRAINT prompt_proposal_change_kind_check;
ALTER TABLE prompt_proposal ADD CONSTRAINT prompt_proposal_change_kind_check
    CHECK (change_kind IN ('add_clause', 'revise_clause', 'remove_clause', 'move_clause', 'migrate_clause'));
