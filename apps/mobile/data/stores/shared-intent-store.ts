/**
 * Cross-screen channel for the Android share-intent flow (RUYI-463), same
 * one-shot handoff shape as chat-session-picker-store / chat-agent-request-
 * store — but the counterpart screens here can't be enumerated by one store
 * consumer: the share landing page (`app/share-target.tsx`) WRITES, and the
 * destination screen (chat tab / new-issue panels) TAKEs, one-shot.
 *
 * Lifecycle:
 *   1. `ShareIntentNavigator` (root layout) receives the payload from the
 *      native module and `setPayload`s the cache-copied files, then pushes
 *      `/share-target`.
 *   2. The landing page lets the user pick workspace → destination (→ agent
 *      for chat) and `setDestination`s the choice right before navigating to
 *      the destination route.
 *   3. The destination screen calls `takeFor(kind)`: files are handed over
 *      only when the destination kind matches, and the whole payload clears
 *      atomically (one consumer per share — no double-attach).
 *
 * Deliberately NOT reset on workspace change (unlike the picker stores): the
 * whole point of this flow is to land the user into a workspace they may not
 * currently be in, so the payload must survive the workspace transition that
 * the landing itself triggers. Stale payloads are bounded by `cancel()` on
 * landing-page dismissal and by process death (memory-only, no persistence).
 */
import { create } from "zustand";
import type { SharedFile } from "@/lib/share-payload";

export type ShareDestination =
  | { kind: "chat"; agentId: string }
  | { kind: "issue" };

export interface SharedIntentTake {
  files: SharedFile[];
  destination: ShareDestination;
}

interface SharedIntentState {
  files: SharedFile[];
  destination: ShareDestination | null;
  setPayload: (files: SharedFile[]) => void;
  setDestination: (destination: ShareDestination) => void;
  /** One-shot handoff: null unless `destination.kind` matches, and any
   *  successful take clears the store so a second consumer can't attach
   *  the same files twice. Generic so the caller gets the destination
   *  narrowed to the requested kind (chat caller reads `agentId`). */
  takeFor: <K extends ShareDestination["kind"]>(
    kind: K,
  ) => (SharedIntentTake & {
    destination: Extract<ShareDestination, { kind: K }>;
  }) | null;
  /** Dismissal path (landing backed out / superseded): drop everything. */
  cancel: () => void;
}

const INITIAL: { files: SharedFile[]; destination: null } = {
  files: [],
  destination: null,
};

export const useSharedIntentStore = create<SharedIntentState>((set, get) => ({
  ...INITIAL,
  setPayload: (files) => set({ files }),
  setDestination: (destination) => set({ destination }),
  takeFor: (kind) => {
    const { files, destination } = get();
    if (!destination || destination.kind !== kind || files.length === 0) {
      return null;
    }
    set({ ...INITIAL });
    // 运行时已由上面的 kind 比对收窄；类型层补一个与签名一致的断言。
    return { files, destination } as SharedIntentTake & {
      destination: Extract<ShareDestination, { kind: typeof kind }>;
    };
  },
  cancel: () => set({ ...INITIAL }),
}));
