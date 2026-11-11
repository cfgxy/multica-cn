/**
 * Mobile-only zustand store for the Decisions tab (RUYI-530, refolded by
 * RUYI-864). The screen keeps only the decision list: the status TAB is now
 * a single-value window selector that travels to the server as the `status`
 * query param (全部 → no param; 待决策/已决策/已失效 → one card status), so
 * the list, the section counts and every empty state follow the same window
 * — the client-side 有推荐 / Agent 发起 sheet toggles are gone with the
 * filter sheet, and paging means a client-side row drop could never stay
 * honest to the server counts anyway.
 *
 * Conventions carried over from the Tasks tab (tasks-view-store.ts):
 *   - The TAB is session-scoped: it rides a module-global store so it
 *     survives the screen's per-workspace remounts, but a cold start opens
 *     全部 — persisted across restarts it would be pure noise.
 *   - syncWorkspace keeps the TAB across a workspace switch (保 TAB); a
 *     workspace switch clears it to 全部 only on the next cold start.
 */
import { create } from "zustand";
import type { DecisionTab } from "@/lib/decision-inbox-display";

interface DecisionsViewState {
  tab: DecisionTab;
  /** Owning workspace of the current view state (see `syncWorkspace`). */
  wsId: string | null;
  setTab: (tab: DecisionTab) => void;
  syncWorkspace: (wsId: string | null) => void;
}

export const useDecisionsViewStore = create<DecisionsViewState>()((set) => ({
  tab: "all",
  wsId: null,
  // The TAB is a window selector, not a filter toggle: switching keeps
  // whatever search keyword the user typed (the two compose as the server
  // filter window) and applies to the whole screen, queue included.
  setTab: (tab) => set({ tab }),
  // Keep the TAB across a real workspace switch (same 保 TAB convention as
  // Tasks) — the status window is a harmless preference to carry over.
  syncWorkspace: (wsId) =>
    set((state) => (state.wsId === wsId ? {} : { wsId })),
}));
