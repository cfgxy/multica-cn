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
  current_step: "questionnaire",
  onboarded_at: null,
  questionnaire: INITIAL_QUESTIONNAIRE,
  workspace_id: null,
  runtime_id: null,
  agent_id: null,
  first_issue_id: null,
  onboarding_project_id: null,
  platform_preference: null,
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
 * Session-local UI state for onboarding — questionnaire drafts, step
 * progress, ephemeral IDs captured as the user moves through the flow.
 *
 * Note: "am I onboarded?" is NOT sourced here. That signal lives on
 * `user.onboarded_at` (auth store), which is persisted server-side.
 * `complete()` hits the server to set that timestamp, then refreshes
 * the auth store so every trigger sees the new state immediately.
 */
export const useOnboardingStore = create<OnboardingStoreValue>((set) => ({
  state: INITIAL_STATE,
  advance: async (patch) => {
    set((s) => ({ state: { ...s.state, ...patch } }));
  },
  complete: async (patch) => {
    await api.markOnboardingComplete();
    await useAuthStore.getState().refreshMe();
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
