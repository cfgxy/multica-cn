/**
 * Comment-highlight timing, shared by all three clients (RUYI-108).
 *
 * Web/desktop and mobile express the flash with completely different
 * machinery — a Tailwind `transition-colors` on a background class vs a
 * Reanimated opacity sequence over an absolute overlay — so the only thing
 * they can actually share is the schedule. Before this file the two drifted:
 * web held 2500ms and then faded for another 700ms (3.2s of visible tint),
 * mobile ran 700+1800+700 (3.2s) while its upstream gate cut the row loose
 * after 5s. The task asks for 1–2s everywhere, and "everywhere" only stays
 * true if one number moves all three.
 *
 * The schedule, from the moment a comment becomes the highlight target:
 *
 *   0 ──── FADE ────► full tint ──── (HOLD - FADE) ────► TOTAL
 *          fade in                    held                fade out ends
 *
 * `HOLD_MS` is when the owner clears the highlight state; the fade-out then
 * costs one more `FADE_MS`, so what the reader actually perceives lasts
 * `TOTAL_MS`. Anything driving its own animation (mobile's overlay) must run
 * for `TOTAL_MS`, not `HOLD_MS` — cutting the sequence at HOLD freezes the
 * overlay mid-fade instead of finishing it.
 *
 * Values are deliberately at the top of the requested band: a flash that is
 * too short is missed entirely when the scroll animation is still settling.
 */

/** Cross-fade duration for both entering and leaving the highlighted state. */
export const COMMENT_HIGHLIGHT_FADE_MS = 300;

/**
 * How long the target stays marked as highlighted. Web/desktop clear their
 * highlight state on this timer; the CSS transition renders the fade-out
 * after it.
 *
 * Web/desktop express `FADE_MS` as the Tailwind class `duration-300`, which
 * must be a literal for the compiler to emit it — grep `duration-300` in
 * `comment-card.tsx` before changing `COMMENT_HIGHLIGHT_FADE_MS`.
 */
export const COMMENT_HIGHLIGHT_HOLD_MS = 1400;

/** Total time the tint is visible, fade-in and fade-out included. */
export const COMMENT_HIGHLIGHT_TOTAL_MS =
  COMMENT_HIGHLIGHT_HOLD_MS + COMMENT_HIGHLIGHT_FADE_MS;
