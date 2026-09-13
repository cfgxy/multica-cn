import { create } from "zustand";
import type { TimelineSortMode } from "@multica/core/issues/timeline-sort";

interface TimelineSortStore {
  mode: TimelineSortMode;
  hintSeen: boolean;
  setMode: (mode: TimelineSortMode) => void;
  setHintSeen: () => void;
}

/** Session-only mobile counterpart of the shared web/desktop preference. */
export const useTimelineSortStore = create<TimelineSortStore>()((set) => ({
  mode: "recent-comment",
  hintSeen: false,
  setMode: (mode) =>
    set((state) => (state.mode === mode ? state : { mode })),
  setHintSeen: () =>
    set((state) => (state.hintSeen ? state : { hintSeen: true })),
}));
