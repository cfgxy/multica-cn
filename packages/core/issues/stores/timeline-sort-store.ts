import { create } from "zustand";
import type { TimelineSortMode } from "../timeline-sort";

interface TimelineSortStore {
  mode: TimelineSortMode;
  hintSeen: boolean;
  setMode: (mode: TimelineSortMode) => void;
  setHintSeen: () => void;
}

/**
 * Shared, session-only display preference for issue comment timelines.
 * It intentionally is not persisted: a new session starts with the product
 * default while web and desktop retain the user's choice during navigation.
 */
export const useTimelineSortStore = create<TimelineSortStore>()((set) => ({
  mode: "created",
  hintSeen: false,
  setMode: (mode) =>
    set((state) => (state.mode === mode ? state : { mode })),
  setHintSeen: () =>
    set((state) => (state.hintSeen ? state : { hintSeen: true })),
}));
