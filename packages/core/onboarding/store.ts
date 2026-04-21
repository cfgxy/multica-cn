"use client";

import { create } from "zustand";
import { api } from "../api";
import { useAuthStore } from "../auth";
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
  current_step: "welcome",
  questionnaire: INITIAL_QUESTIONNAIRE,
};

interface OnboardingStoreValue {
  state: OnboardingState;
  advance: (patch: Partial<OnboardingState>) => Promise<void>;
  complete: () => Promise<void>;
  reset: () => void;
}

/**
 * Session-local UI state for the onboarding state machine:
 *   - current_step: where the user is in the flow
 *   - questionnaire: Q1/Q2/Q3 draft answers
 *
 * "Am I onboarded?" does NOT live here. That signal is
 * user.onboarded_at on the auth store (server-persisted).
 */
export const useOnboardingStore = create<OnboardingStoreValue>((set) => ({
  state: INITIAL_STATE,
  advance: async (patch) => {
    set((s) => ({ state: { ...s.state, ...patch } }));
  },
  complete: async () => {
    await api.markOnboardingComplete();
    await useAuthStore.getState().refreshMe();
    set((s) => ({ state: { ...s.state, current_step: null } }));
  },
  reset: () => set({ state: INITIAL_STATE }),
}));
