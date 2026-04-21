ALTER TABLE "user"
  ADD COLUMN onboarding_current_step TEXT,
  ADD COLUMN onboarding_questionnaire JSONB NOT NULL DEFAULT '{}'::jsonb;
