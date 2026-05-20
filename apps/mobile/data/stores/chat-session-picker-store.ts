/**
 * Cross-screen channel between the chat tab (`(tabs)/chat.tsx`) and the
 * `chat-sessions` formSheet route. The formSheet can't push selections back
 * up the React tree (different routes), so we hand off through a small
 * store with three slots:
 *
 *   - `activeSessionId` — mirrored from the chat tab so the picker can
 *     render the current selection's check mark. The chat tab calls
 *     `setActiveSessionId` whenever its local state changes.
 *   - `selectRequest` — the picker writes the id (or null) the user picked;
 *     the chat tab `useEffect`s on it, applies it, then `consume()`s.
 *   - `openAgentPickerRequest` — bumped when the user taps "Switch agent"
 *     so the chat tab can open its (still-modal) agent picker after the
 *     formSheet dismisses.
 *
 * Both request slots are one-shot (consumed after read). This avoids
 * re-firing on every render or after a soft navigation back.
 */
import { create } from "zustand";

interface ChatSessionPickerState {
  activeSessionId: string | null;
  setActiveSessionId: (id: string | null) => void;
  selectRequest: { id: string | null; nonce: number } | null;
  requestSelect: (id: string | null) => void;
  openAgentPickerRequest: { nonce: number } | null;
  requestNewWithAgent: () => void;
  consumeSelect: () => void;
  consumeOpenAgentPicker: () => void;
}

export const useChatSessionPickerStore = create<ChatSessionPickerState>(
  (set, get) => ({
    activeSessionId: null,
    setActiveSessionId: (id) => set({ activeSessionId: id }),
    selectRequest: null,
    requestSelect: (id) =>
      set({ selectRequest: { id, nonce: (get().selectRequest?.nonce ?? 0) + 1 } }),
    openAgentPickerRequest: null,
    requestNewWithAgent: () =>
      set({
        openAgentPickerRequest: {
          nonce: (get().openAgentPickerRequest?.nonce ?? 0) + 1,
        },
      }),
    consumeSelect: () => set({ selectRequest: null }),
    consumeOpenAgentPicker: () => set({ openAgentPickerRequest: null }),
  }),
);
