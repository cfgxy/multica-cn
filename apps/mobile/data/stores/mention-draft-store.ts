/**
 * Cross-route draft store for the comment composer's @mention chips.
 *
 * The mention picker route (`app/(app)/[workspace]/issue/[id]/picker/mention.tsx`)
 * lives in its own formSheet and cannot share callbacks with the composer
 * that opened it. Same pattern as how label / assignee pickers
 * communicate with their issue-detail screen — except those write straight
 * to a mutation (durable state), while this store holds purely client-side
 * draft state until the composer either sends or unmounts.
 *
 * Scope is intentionally narrow: ONE slot (mentions). Attachments stay as
 * local composer state because no route outside the composer needs to
 * touch them.
 *
 * Lifecycle: composer's `useEffect` cleanup calls `clear()` so navigating
 * to another issue starts with an empty mention array. Send success also
 * clears.
 */
import { create } from "zustand";
import type { MentionToken } from "@/lib/mention-trigger";

export type MentionTargetType = "member" | "agent" | "squad" | "all" | "issue";

export interface MentionChipDraft {
  type: MentionTargetType;
  /** UUID for member / agent / squad / issue; literal "all" for @all. */
  id: string;
  /** Display name without leading `@`. For type "issue" this stores the
   *  human identifier (e.g. "MUL-123"). */
  name: string;
}

function sameMention(
  a: MentionChipDraft,
  b: { type: MentionTargetType; id: string },
) {
  return a.type === b.type && a.id === b.id;
}

interface State {
  mentions: MentionChipDraft[];
  /** Add or remove by (type, id). Kept for MessageComposer's submit
   *  rollback, which restores chips into a just-cleared store (there
   *  toggle === add). */
  toggle: (mention: MentionChipDraft) => void;
  /** RUYI-232 single-select insert: add the chip idempotently. The
   *  picker calls this once per picked row and closes the sheet
   *  immediately — re-picking the same row must not remove it. */
  add: (mention: MentionChipDraft) => void;
  remove: (type: MentionTargetType, id: string) => void;
  clear: () => void;
  /** RUYI-232 typing trigger: the `@` token the composer was editing
   *  when it pushed the picker, so the composer can strip the raw
   *  `@<query>` text from the draft once a pick lands. Null when the
   *  picker was opened via the `@` toolbar button (no token to strip).
   *  Overwritten on every composer-initiated push, so a stale value
   *  from a dismissed sheet can never mis-fire a later strip. */
  token: MentionToken | null;
  setToken: (token: MentionToken | null) => void;
}

export const useMentionDraftStore = create<State>((set) => ({
  mentions: [],
  toggle: (mention) =>
    set((s) => {
      const existing = s.mentions.some((m) => sameMention(m, mention));
      if (existing) {
        return {
          mentions: s.mentions.filter((m) => !sameMention(m, mention)),
        };
      }
      return { mentions: [...s.mentions, mention] };
    }),
  add: (mention) =>
    set((s) =>
      s.mentions.some((m) => sameMention(m, mention))
        ? s
        : { mentions: [...s.mentions, mention] },
    ),
  remove: (type, id) =>
    set((s) => ({
      mentions: s.mentions.filter((m) => !sameMention(m, { type, id })),
    })),
  clear: () => set({ mentions: [] }),
  token: null,
  setToken: (token) => set({ token }),
}));
