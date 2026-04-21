"use client";

import { create } from "zustand";
import type { OnboardingState, QuestionnaireAnswers } from "./types";

const INITIAL_QUESTIONNAIRE: QuestionnaireAnswers = {
  team_size: null,
  team_size_other: null,
  role: null,
  role_other: null,
  use_case: null,
  use_case_other: null,
};

const INITIAL_STATE: OnboardingState = {
  current_step: "questionnaire",
  onboarded_at: null,
  questionnaire: INITIAL_QUESTIONNAIRE,
  workspace_id: null,
  runtime_id: null,
  agent_id: null,
  first_issue_id: null,
  onboarding_project_id: null,
  platform_preference: null,
  cloud_waitlist_email: null,
  cloud_waitlist_description: null,
};

interface OnboardingStoreValue {
  state: OnboardingState;
  advance: (patch: Partial<OnboardingState>) => Promise<void>;
  complete: (patch: {
    first_issue_id?: string;
    onboarding_project_id?: string;
  }) => Promise<void>;
  reset: () => void;
}

/**
 * Dev-phase onboarding store. **Not persisted** — every fresh page load
 * starts at Step 1, which is intentional during frontend development:
 * we want to re-enter the flow on every login to iterate on each step
 * without manual reset.
 *
 * When the backend `user_onboarding` table ships, replace the
 * implementation inside this file with a TanStack Query fetch +
 * PATCH /api/me/onboarding mutation. The exported interface
 * (`advance`, `complete`, `reset`) stays stable so consumer components
 * do not change. `advance`/`complete` already return `Promise<void>`
 * for this reason — today they resolve synchronously, later they'll
 * be real network round-trips.
 */
export const useOnboardingStore = create<OnboardingStoreValue>((set) => ({
  state: INITIAL_STATE,
  advance: async (patch) => {
    set((s) => ({ state: { ...s.state, ...patch } }));
  },
  complete: async (patch) => {
    set((s) => ({
      state: {
        ...s.state,
        ...patch,
        current_step: null,
        onboarded_at: new Date().toISOString(),
      },
    }));
  },
  reset: () => set({ state: INITIAL_STATE }),
}));
