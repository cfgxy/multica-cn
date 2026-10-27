/**
 * Cross-screen channel for "DM this agent" (RUYI-418 A7): the agent detail
 * screen requests the chat tab to open a fresh session with a given agent.
 * The chat tab is a different Stack screen from the agent detail screen, so
 * the handoff rides a small Zustand store with a one-shot,
 * nonce-stamped request consumed by the chat tab's effect.
 *
 * Permission gating happens at the sender (detail screen alerts on a denied
 * invocation); this store carries no decision of its own. Reset on
 * workspace change, wired from `app/(app)/[workspace]/_layout.tsx`.
 */
import { useEffect, useRef } from "react";
import { create } from "zustand";

interface ChatAgentRequestState {
  agentRequest: { id: string; nonce: number } | null;
  requestAgent: (id: string) => void;
  consumeAgent: () => void;
  reset: () => void;
}

const INITIAL = {
  agentRequest: null,
} as const;

export const useChatAgentRequestStore = create<ChatAgentRequestState>(
  (set, get) => ({
    ...INITIAL,
    requestAgent: (id) =>
      set({
        agentRequest: { id, nonce: (get().agentRequest?.nonce ?? 0) + 1 },
      }),
    consumeAgent: () => set({ agentRequest: null }),
    reset: () => set({ ...INITIAL }),
  }),
);

/**
 * Clears the request whenever the active workspace id changes. Mounted once
 * from the workspace `_layout.tsx`; the ref gate keeps the first mount a
 * no-op (same pattern as the other cross-screen stores).
 */
export function useChatAgentRequestResetOnWorkspaceChange(wsId: string | null) {
  const prevRef = useRef(wsId);
  useEffect(() => {
    if (prevRef.current === wsId) return;
    prevRef.current = wsId;
    useChatAgentRequestStore.getState().reset();
  }, [wsId]);
}
