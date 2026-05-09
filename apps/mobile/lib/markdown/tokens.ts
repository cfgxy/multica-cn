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

/** Base paragraph leading; paired with text-sm for ~140% density. */
export const PARAGRAPH_LEADING = "leading-5";

/** Per-level indent for nested lists, in pixels (View paddingLeft). */
export const LIST_INDENT = 16;

/** Tailwind classes per heading depth (1 = h1, 6 = h6). */
export const HEADING_CLASS: Record<number, string> = {
  1: "text-2xl font-bold",
  2: "text-xl font-bold",
  3: "text-lg font-semibold",
  4: "text-base font-semibold",
  5: "text-sm font-semibold",
  6: "text-xs font-semibold uppercase tracking-wide",
};

/** Body / paragraph text default. */
export const BODY_CLASS = "text-sm text-foreground";

/** Inline code (within a paragraph) — distinct background, monospace. */
export const INLINE_CODE_CLASS =
  "text-sm font-mono bg-muted px-1 py-0.5 rounded";

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
