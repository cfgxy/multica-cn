ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS value_score;
ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS expected_gain;
ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS expected_effect;
ALTER TABLE prompt_proposal DROP COLUMN IF EXISTS problem;
