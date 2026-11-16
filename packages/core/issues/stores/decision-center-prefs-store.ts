"use client";

import { createStore, type StoreApi } from "zustand/vanilla";
import { createJSONStorage, persist } from "zustand/middleware";
import type { IssueDecisionStatus } from "../../types";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

// Decision Center display prefs: the single status window the page is
// showing. "all" or exactly one card status (a 1:1 real state — request
// lifecycle statuses are never folded onto a card). Persisted per
// workspace like every other surface's view state.
//
// RUYI-864: the board view and the multi-status "hide" filter both went
// away — the list is the only view, and the filter is a single-value status
// window that maps straight onto the server's `status` query param. So the
// v1 payload (`viewMode` + `hiddenStatuses`) is fully dropped at the storage
// boundary; version 2 starts from the "all" default.

export type DecisionCenterStatusFilter = "all" | IssueDecisionStatus;

export interface DecisionCenterPrefsState {
  /** The one status window the page is on; "all" lifts the window. */
  statusFilter: DecisionCenterStatusFilter;
  setStatusFilter: (filter: DecisionCenterStatusFilter) => void;
}

const _decisionCenterPrefsStore = createStore<DecisionCenterPrefsState>()(
  persist(
    (set) => ({
      statusFilter: "all",
      setStatusFilter: (statusFilter) => set({ statusFilter }),
    }),
    {
      name: "multica_decision_center_prefs",
      version: 2,
      // v1 → v2: `viewMode` (the board toggle) and `hiddenStatuses` (the
      // multi-status hide model) are both gone, so every legacy payload is
      // forgotten and the store reopens on the "all" window. No field is
      // carried across — a previously hidden status no longer has meaning
      // in the single-value model.
      migrate: () => ({ statusFilter: "all" }),
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
    },
  ),
);

export const decisionCenterPrefsStore: StoreApi<DecisionCenterPrefsState> =
  _decisionCenterPrefsStore;

registerForWorkspaceRehydration(() => _decisionCenterPrefsStore.persist.rehydrate());
