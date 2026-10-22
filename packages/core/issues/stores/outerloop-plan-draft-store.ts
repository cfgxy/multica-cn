import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { registerDraftCleanup } from "../../drafts/cleanup-registry";
import { defaultStorage } from "../../platform/storage";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";

export interface OuterLoopPlanDraft {
  goal: string;
  maxRounds: number;
}

interface OuterLoopPlanDraftState {
  drafts: Record<string, OuterLoopPlanDraft>;
  setDraft: (issueId: string, draft: OuterLoopPlanDraft) => void;
  clearDraft: (issueId: string) => void;
}

export const useOuterLoopPlanDraftStore = create<OuterLoopPlanDraftState>()(
  persist(
    (set) => ({
      drafts: {},
      setDraft: (issueId, draft) => set((state) => ({
        drafts: { ...state.drafts, [issueId]: draft },
      })),
      clearDraft: (issueId) => set((state) => {
        if (!(issueId in state.drafts)) return state;
        const drafts = { ...state.drafts };
        delete drafts[issueId];
        return { drafts };
      }),
    }),
    {
      name: "multica_outerloop_plan_drafts",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
    },
  ),
);

registerForWorkspaceRehydration(() => useOuterLoopPlanDraftStore.persist.rehydrate());
registerDraftCleanup({
  storageKey: "multica_outerloop_plan_drafts",
  workspaceScoped: true,
  resetInMemory: () => useOuterLoopPlanDraftStore.setState({ drafts: {} }),
});
