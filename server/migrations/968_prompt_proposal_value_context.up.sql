-- RUYI-723: owner-facing decision context on the proposal pool.
--
-- The owner approving a draft needs to see what problem it solves, what
-- effect and gain to expect, and a reference value score — the retrospective
-- contract (server/internal/retrospective) starts producing them with this
-- migration. All four columns are nullable: rows written before this
-- migration and manual proposals carry no values and the UI renders "—".
-- The value score is a reference signal for triage, never an acceptance
-- gate; the CHECK keeps the 1-5 contract at the storage layer as well.

ALTER TABLE prompt_proposal ADD COLUMN problem TEXT;
ALTER TABLE prompt_proposal ADD COLUMN expected_effect TEXT;
ALTER TABLE prompt_proposal ADD COLUMN expected_gain TEXT;
ALTER TABLE prompt_proposal ADD COLUMN value_score INT
    CONSTRAINT prompt_proposal_value_score_range
    CHECK (value_score IS NULL OR (value_score >= 1 AND value_score <= 5));

COMMENT ON COLUMN prompt_proposal.problem IS
    'RUYI-723: what friction this draft removes, short text (≤100 chars from the retrospective contract); NULL for manual proposals and pre-723 rows.';
COMMENT ON COLUMN prompt_proposal.expected_effect IS
    'RUYI-723: the expected effect once enacted, short text (≤100 chars); NULL = unset.';
COMMENT ON COLUMN prompt_proposal.expected_gain IS
    'RUYI-723: how large the expected gain is, short text (≤100 chars); NULL = unset.';
COMMENT ON COLUMN prompt_proposal.value_score IS
    'RUYI-723: 1-5 integer reference score (5 = caused a delivery rollback/blocking or repeated cross-issue friction; 4 = one rework or acceptance failure; 3 = notable verification cost; 2 = minor improvement; 1 = nice-to-have); NULL = unrated. Reference only, never a gate.';
