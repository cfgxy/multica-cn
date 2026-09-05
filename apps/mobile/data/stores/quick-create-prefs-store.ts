/**
 * Session-scoped preferences for the smart-mode (agent quick-create) flow,
 * mirroring two web stores: create-mode-store (`lastMode`) and
 * quick-create-store (`lastActor`). Mobile keeps them in one store because
 * they share a single consumer (the new-issue screen) and the mobile store
 * convention is in-memory only (see my-issues-view-store.ts — no persist
 * middleware v1; restart survival is a deliberate mobile divergence from
 * web's localStorage-backed preference, not a semantic one).
 *
 * `lastActor` is workspace-agnostic on mobile v1: it seeds only a
 * suggestion that must resolve against the current workspace's visible
 * agents/squads (`resolveQuickCreateActor`), so a stale id from another
 * workspace falls through to the first visible agent — the same fail-soft
 * chain web runs per workspace.
 */
import { create } from "zustand";
import type { QuickCreateActorRef } from "@/lib/quick-create";

export type QuickCreateMode = "smart" | "manual";

interface QuickCreatePrefsState {
  lastMode: QuickCreateMode;
  lastActor: QuickCreateActorRef | null;
  setLastMode: (mode: QuickCreateMode) => void;
  setLastActor: (actor: QuickCreateActorRef | null) => void;
}

export const useQuickCreatePrefsStore = create<QuickCreatePrefsState>(
  (set) => ({
    // Web's create-mode-store also defaults to "agent" (smart) — a fresh
    // user lands on the one-line flow first.
    lastMode: "smart",
    lastActor: null,
    setLastMode: (mode) => set({ lastMode: mode }),
    setLastActor: (actor) => set({ lastActor: actor }),
  }),
);
