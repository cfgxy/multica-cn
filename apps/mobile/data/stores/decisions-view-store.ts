/**
 * Mobile-only zustand store for the Decisions tab (RUYI-530) — the TAB and
 * filter mechanics mirror the Tasks tab's conventions (tasks-view-store.ts),
 * trimmed to the two card-owned filter dimensions:
 *
 *   - TAB switches keep the filters (they apply on every TAB — the dispatch
 *     AC 「切换保留筛选状态」); clearFilters keeps the current TAB.
 *   - Filters are workspace-scoped: syncWorkspace detects a real switch via
 *     the owning wsId in the state and clears the filters, keeping the TAB.
 *   - Persistence mirrors Tasks: the filter memory (with its owning wsId)
 *     rides in AsyncStorage across cold starts; the TAB stays session-
 *     scoped — a restart opens 全部.
 */
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import AsyncStorage from "@react-native-async-storage/async-storage";
import type { DecisionTab } from "@/lib/decision-inbox-display";

interface DecisionsViewState {
  tab: DecisionTab;
  /** Owning workspace of the current view state (see `syncWorkspace`). */
  wsId: string | null;
  /** 仅看有推荐 — cards carrying a recommendation. */
  recommendedOnly: boolean;
  /** 仅看 Agent 发起 — cards raised by an agent, not a member. */
  agentCreatedOnly: boolean;
  setTab: (tab: DecisionTab) => void;
  syncWorkspace: (wsId: string | null) => void;
  toggleRecommendedOnly: () => void;
  toggleAgentCreatedOnly: () => void;
  clearFilters: () => void;
}

export const useDecisionsViewStore = create<DecisionsViewState>()(
  persist(
    (set) => ({
      tab: "all",
      wsId: null,
      recommendedOnly: false,
      agentCreatedOnly: false,
      // Deliberately does not touch the filters: they apply on every TAB
      // and must survive a round-trip through another pill (dispatch AC).
      setTab: (tab) => set({ tab }),
      // Filters are workspace-scoped; a real switch clears them and keeps
      // the TAB (保 TAB). The cleared state is what persist writes.
      syncWorkspace: (wsId) =>
        set((state) =>
          state.wsId === wsId
            ? {}
            : { wsId, recommendedOnly: false, agentCreatedOnly: false },
        ),
      toggleRecommendedOnly: () =>
        set((state) => ({ recommendedOnly: !state.recommendedOnly })),
      toggleAgentCreatedOnly: () =>
        set((state) => ({ agentCreatedOnly: !state.agentCreatedOnly })),
      // 重置 clears every filter but keeps the TAB.
      clearFilters: () =>
        set({ recommendedOnly: false, agentCreatedOnly: false }),
    }),
    {
      name: "multica_mobile_decisions_view",
      storage: createJSONStorage(() => AsyncStorage),
      // TAB stays session-scoped — only the filter memory (with its owning
      // workspace) survives a restart.
      partialize: (s) => ({
        wsId: s.wsId,
        recommendedOnly: s.recommendedOnly,
        agentCreatedOnly: s.agentCreatedOnly,
      }),
      // Restore-scoping guard, same semantics as tasks-view-store: a blob
      // may only come back under the workspace it was written for.
      merge: (persisted, current) => {
        const p = persisted as Partial<DecisionsViewState> | null;
        if (!p || typeof p.wsId !== "string") return current;
        if (current.wsId !== null && current.wsId !== p.wsId) return current;
        return { ...current, ...p };
      },
    },
  ),
);
