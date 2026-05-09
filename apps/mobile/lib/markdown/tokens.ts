/**
 * Design tokens for the mobile markdown renderer. Single source for spacing,
 * heading scale, and color choices so the renderer stays visually consistent
 * and tweaks happen in one place.
 *
 * All values stick to Tailwind's built-in scale (no `text-[Npx]` arbitrary
 * values — see project memory feedback_no_arbitrary_px.md).
 */

/** Vertical breathing room below each block-level node. */
export const BLOCK_GAP = "mb-3";

/** Extra margin above headings to break up dense content. */
export const HEADING_TOP_GAP = "mt-4";

/** Base paragraph leading. text-sm + leading-6 = 24/14 ≈ 1.71, the right
 *  density for mixed CJK + Latin per Apple's typography guidance — PingFang
 *  SC has taller glyphs than SF, so 1.4-ish (leading-5) feels cramped on
 *  Chinese content. Don't drop below leading-6 in markdown bodies. */
export const PARAGRAPH_LEADING = "leading-6";

/** Per-level indent for nested lists, in pixels (View paddingLeft). */
export const LIST_INDENT = 16;

/** Tailwind classes per heading depth (1 = h1, 6 = h6). Calibrated against
 *  Apple HIG Dynamic Type (Title 3 = 20px / Title 2 = 22px / Body = 17pt)
 *  and verified against GitHub mobile / Linear iOS — markdown headings
 *  inside an issue card are STRUCTURAL, not screen titles, so the whole
 *  scale lives one tier below shadcn's web defaults. text-2xl (24px) is
 *  reserved for screen titles, never markdown content. */
export const HEADING_CLASS: Record<number, string> = {
  1: "text-xl font-bold",
  2: "text-lg font-semibold",
  3: "text-base font-semibold",
  4: "text-sm font-semibold",
  5: "text-sm font-semibold",
  6: "text-xs font-semibold uppercase tracking-wide",
};

/** Body / paragraph text default. leading-6 baked in so any one-off Text
 *  using BODY_CLASS without PARAGRAPH_LEADING still gets CJK-friendly
 *  line height. */
export const BODY_CLASS = "text-sm leading-6 text-foreground";

/** Inline code (within a paragraph). RN's nested-Text engine flattens
 *  inline `<Text>` into a single NSAttributedString run on iOS, which
 *  does NOT support border / padding / margin — applying them either
 *  silently drops the property (Paper) or forces the run to break out
 *  of inline flow (Fabric / New Arch). The latter is catastrophic in
 *  CJK paragraphs because UAX #14 then breaks at every adjacent
 *  ideograph. Web's 3-layer chip (bg + border + opacity) HAS NO RN
 *  EQUIVALENT — compensate with a stronger background opacity instead.
 *
 *  See `apps/mobile/docs/markdown-renderer-research.md` → "RN native
 *  rendering constraints" for primary sources (RN #10775, #45925, #6728).
 *
 *  DO NOT add border, padding, or vertical margin here. Ever. */
export const INLINE_CODE_CLASS =
  "text-sm font-mono text-foreground/75 bg-foreground/10 px-1 rounded";

/** Block code (fenced ``` blocks). */
export const CODE_BLOCK_TEXT_CLASS = "text-sm font-mono text-foreground";
export const CODE_BLOCK_CONTAINER_CLASS = "bg-muted rounded-lg p-3";
export const CODE_BLOCK_LANG_LABEL_CLASS =
  "text-xs uppercase tracking-wide text-muted-foreground mb-1";

/** Plain (non-mention) link styling. */
export const LINK_CLASS = "text-primary underline";

/** Mention chip styling — distinct color per type so users can tell at a
 *  glance whether it's a person, an agent, or an issue link. */
export const MENTION_MEMBER_CLASS = "text-primary font-medium";
export const MENTION_AGENT_CLASS = "text-brand font-medium";
export const MENTION_ISSUE_CLASS = "text-info font-medium";

/** Block quote left bar. */
export const QUOTE_BORDER_CLASS = "border-l-2 border-border pl-3";

/** Horizontal rule. */
export const HR_CLASS = "border-b border-border";
