/**
 * Trigger predicate for "typing `@` should open the mention picker"
 * (RUYI-232). Companion to `tokenAtCursor` in mention-serialize.ts:
 * `tokenAtCursor` answers "is the cursor inside a mention token right
 * now" (word-boundary check), while this answers "did THIS input event
 * just start a token" — the distinction matters because the picker is a
 * formSheet, so we must push it exactly once per newly-typed `@`, not on
 * every keystroke inside an already-open token.
 *
 * Owner constraint: only a `@` preceded by whitespace (or at line start)
 * may trigger, so typing an email like `a@b` must never open the picker.
 * The boundary half lives in `tokenAtCursor`; this module adds the
 * "freshly typed" half.
 */
import { tokenAtCursor } from "@/lib/mention-serialize";

/** A freshly-typed `@` token: offset of the `@` in the draft text and
 *  the query typed after it. Same shape as `tokenAtCursor`'s hit. */
export interface MentionToken {
  start: number;
  query: string;
}

/**
 * Returns the mention token started by this input event, or null when
 * the event should NOT open the picker.
 *
 * @param prevText  Text before the input event was applied.
 * @param nextText  Text after the input event was applied.
 * @param prevCursor Caret offset (end) in `prevText` before the event.
 *
 * Rules:
 *   1. Deletions and same-length replacements never trigger.
 *   2. The caret is estimated forward by the length delta — exact for
 *      plain typing at the caret, and close enough for paste because
 *      rules 3/4 re-scan the full next text.
 *   3. The inserted slice must contain `@` (cheap pre-filter).
 *   4. `tokenAtCursor(nextText, cursor)` must hit, AND the token's `@`
 *      must sit inside the inserted slice (`token.start >= prevCursor`)
 *      — so continuing to type inside an existing `@token` ("hi @jo" →
 *      "hi @john") never re-triggers.
 */
export function mentionTriggerFromInput(
  prevText: string,
  nextText: string,
  prevCursor: number,
): MentionToken | null {
  const added = nextText.length - prevText.length;
  if (added <= 0) return null;

  const cursor = Math.min(Math.max(prevCursor + added, 0), nextText.length);
  const inserted = nextText.slice(prevCursor, cursor);
  if (!inserted.includes("@")) return null;

  const token = tokenAtCursor(nextText, cursor);
  if (!token || token.start < prevCursor) return null;
  return token;
}
