"use client";

import { api } from "../api";
import { useAuthStore } from "../auth";
import type { OnboardingStep, QuestionnaireAnswers } from "./types";

/**
 * Persist onboarding progress on the server and sync the refreshed
 * user object into the auth store. Callers can pass either field
 * independently:
 *   - `current_step` → save the user's furthest reached step
 *   - `questionnaire` → save Q1/Q2/Q3 answers
 *
 * Source of truth for both lives on `user.onboarding_current_step`
 * and `user.onboarding_questionnaire` (auth store). No client-side
 * cache here.
 */
export async function advanceOnboarding(patch: {
  current_step?: OnboardingStep;
  questionnaire?: Partial<QuestionnaireAnswers>;
}): Promise<void> {
  const user = await api.patchOnboarding(patch);
  useAuthStore.getState().setUser(user);
}

/**
 * Finalize onboarding. POST /complete marks `onboarded_at` atomically
 * (COALESCE-guarded for idempotency) and clears `current_step`. We
 * then refresh the auth store so every gate sees the updated user.
 */
export async function completeOnboarding(): Promise<void> {
  await api.markOnboardingComplete();
  await useAuthStore.getState().refreshMe();
}
