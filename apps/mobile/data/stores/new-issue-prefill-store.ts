/**
 * One-shot prefill channel from the inbox quick-create outcome detail into
 * the new-issue screen (RUYI-605) — the same handoff shape as
 * shared-intent-store (RUYI-463) and chat-agent-request-store: the inbox
 * detail screen WRITES, the new-issue screen TAKEs exactly once.
 *
 * Why a store instead of route params:
 *   - `original_prompt` is arbitrary multi-line user text; a store keeps it
 *     out of the router's param serialization.
 *   - The new-issue screen can be reused without remount on some landing
 *     paths (the hazard RUYI-463 solved by consuming on focus) — a one-shot
 *     store decouples the handoff from navigation mechanics.
 *   - The draft store is the wrong home: new-issue resets it on mount, and
 *     description deliberately does not live there (see
 *     new-issue-draft-store.ts) — the seed must SURVIVE that reset, so it
 *     rides a separate channel consumed after the reset.
 *
 * Consumption order is load-bearing (new-issue.tsx): mount `resetDraft()`
 * runs first (useLayoutEffect), the take runs second — the seed lands in a
 * clean draft — and the take result gates the last-assignee memory seed so
 * an explicit agent prefill wins over remembered history (web parity: the
 * edit-advanced seed writes the assignee explicitly).
 *
 * Memory-only, no persistence: a seed that never got taken dies with the
 * process, and the next new-issue mount consumes it (one consumer per seed).
 */
import { create } from "zustand";
import type { QuickCreateEditSeed } from "@/lib/quick-create-edit";

interface NewIssuePrefillState {
  seed: QuickCreateEditSeed | null;
  seedNewIssuePrefill: (seed: QuickCreateEditSeed) => void;
  /** One-shot: returns the seed and clears the store; null when absent. */
  takeNewIssuePrefill: () => QuickCreateEditSeed | null;
}

export const useNewIssuePrefillStore = create<NewIssuePrefillState>(
  (set, get) => ({
    seed: null,
    seedNewIssuePrefill: (seed) => set({ seed }),
    takeNewIssuePrefill: () => {
      const { seed } = get();
      if (!seed) return null;
      set({ seed: null });
      return seed;
    },
  }),
);

/** Record the recovery seed before navigating to the new-issue screen. */
export function seedNewIssuePrefill(seed: QuickCreateEditSeed): void {
  useNewIssuePrefillStore.getState().seedNewIssuePrefill(seed);
}

/** Consume the pending seed (one-shot) — the new-issue screen's entry point. */
export function takeNewIssuePrefill(): QuickCreateEditSeed | null {
  return useNewIssuePrefillStore.getState().takeNewIssuePrefill();
}
