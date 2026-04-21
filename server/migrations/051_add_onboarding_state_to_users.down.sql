ALTER TABLE "user"
  DROP COLUMN IF EXISTS onboarding_questionnaire,
  DROP COLUMN IF EXISTS onboarding_current_step;
