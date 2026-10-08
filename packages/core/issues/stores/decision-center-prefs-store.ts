"use client";

import { createStore, type StoreApi } from "zustand/vanilla";
import { createJSONStorage, persist } from "zustand/middleware";
import type { IssueDecisionStatus } from "../../types";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

// Decision Center display prefs (RUYI-547): which view is open and which
// decision statuses are filtered out. Small on purpose — the page has no
// saved views, grouping or sort, so this is the issues view-store pattern
// reduced to the two axes the page actually owns. Persisted per workspace
// like every other surface's view state.

export type DecisionCenterViewMode = "list" | "board";

export interface DecisionCenterPrefsState {
  viewMode: DecisionCenterViewMode;
  hiddenStatuses: IssueDecisionStatus[];
  setViewMode: (mode: DecisionCenterViewMode) => void;
  toggleStatusHidden: (status: IssueDecisionStatus) => void;
  showAllStatuses: () => void;
}

const _decisionCenterPrefsStore = createStore<DecisionCenterPrefsState>()(
  persist(
    (set) => ({
      viewMode: "list",
      hiddenStatuses: [],
      setViewMode: (viewMode) => set({ viewMode }),
      toggleStatusHidden: (status) =>
        set((state) => ({
          hiddenStatuses: state.hiddenStatuses.includes(status)
            ? state.hiddenStatuses.filter((s) => s !== status)
            : [...state.hiddenStatuses, status],
        })),
      showAllStatuses: () => set({ hiddenStatuses: [] }),
    }),
    {
      name: "multica_decision_center_prefs",
      version: 1,
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
    },
  ),
);

export const decisionCenterPrefsStore: StoreApi<DecisionCenterPrefsState> =
  _decisionCenterPrefsStore;

registerForWorkspaceRehydration(() => _decisionCenterPrefsStore.persist.rehydrate());
