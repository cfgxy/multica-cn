/**
 * `markdownStyle` prop value for `EnrichedMarkdownText`. Mirrors the design
 * tokens in `apps/mobile/tailwind.config.js` — single source of truth for
 * colors lives there; this file is the bridge from those Tailwind tokens
 * to the imperative style object md4c expects.
 *
 * Sizing follows the mobile typography scale documented in
 * `apps/mobile/docs/markdown-renderer-research.md` → "Mobile typography
 * scale" (calibrated against Apple HIG; one tier below shadcn web defaults
 * because markdown headings inside an issue card are structural, not
 * screen titles).
 *
 * Dark mode: mobile is currently single-theme (light tokens only in
 * tailwind.config.js). When dark tokens land, branch on `useColorScheme()`
 * inside `markdown.tsx` and pick the right object.
 */

// Tailwind tokens (kept in sync by hand with apps/mobile/tailwind.config.js)
const FOREGROUND = "#1f1f23";
const MUTED_FOREGROUND = "#71717a";
const MUTED = "#f4f4f5";
const BORDER = "#e4e4e7";
const BRAND = "#4571e0";

export const MARKDOWN_STYLE = {
  // Body / paragraph — text-sm + leading-6 ≈ 1.71. Generous for CJK.
  paragraph: {
    fontSize: 14,
    lineHeight: 24,
    color: FOREGROUND,
    marginBottom: 12,
  },
  // Headings — Apple HIG-calibrated, one tier below shadcn web defaults.
  h1: {
    fontSize: 20,
    fontWeight: "700" as const,
    color: FOREGROUND,
    marginTop: 16,
    marginBottom: 8,
  },
  h2: {
    fontSize: 18,
    fontWeight: "600" as const,
    color: FOREGROUND,
    marginTop: 16,
    marginBottom: 8,
  },
  h3: {
    fontSize: 16,
    fontWeight: "600" as const,
    color: FOREGROUND,
    marginTop: 12,
    marginBottom: 6,
  },
  h4: {
    fontSize: 14,
    fontWeight: "600" as const,
    color: FOREGROUND,
    marginTop: 12,
    marginBottom: 6,
  },
  h5: {
    fontSize: 14,
    fontWeight: "600" as const,
    color: FOREGROUND,
    marginTop: 12,
    marginBottom: 6,
  },
  h6: {
    fontSize: 12,
    fontWeight: "600" as const,
    color: MUTED_FOREGROUND,
    marginTop: 12,
    marginBottom: 6,
  },
  strong: {
    // md4c restricts inline `fontWeight` to "bold" | "normal" — it adds the
    // bold trait on top of the inherited block font. We can't pin a 600
    // weight here the way we can on headings.
    fontWeight: "bold" as const,
  },
  link: {
    color: BRAND,
    underline: true,
  },
  // Inline code — bg + monospace. md4c renders this natively into
  // NSAttributedString attributes, so the chip stays inline (none of the
  // RN nested-Text bugs from the previous walker).
  code: {
    fontSize: 13,
    color: FOREGROUND,
    backgroundColor: "#0000001a", // foreground/10 in tailwind opacity terms
    borderColor: "#00000026", // foreground/15 — md4c gives us border for free
  },
  // Block code — bigger box, muted background, mono font.
  codeBlock: {
    fontSize: 13,
    color: FOREGROUND,
    backgroundColor: MUTED,
    padding: 12,
    borderRadius: 8,
    marginBottom: 12,
  },
  // Blockquote — subtle left bar in muted-foreground.
  blockquote: {
    borderColor: BORDER,
    borderWidth: 2,
    backgroundColor: "transparent",
    marginBottom: 12,
  },
  // List — bullets in muted-foreground so they don't compete with content.
  list: {
    fontSize: 14,
    bulletColor: MUTED_FOREGROUND,
    bulletSize: 4,
    markerColor: MUTED_FOREGROUND,
    gapWidth: 8,
    marginLeft: 16,
  },
  image: {
    borderRadius: 8,
    marginBottom: 12,
  },
  taskList: {
    checkedColor: BRAND,
    borderColor: BORDER,
    checkmarkColor: "#ffffff",
    checkboxSize: 16,
  },
  // GFM tables.
  table: {
    fontSize: 14,
    borderColor: BORDER,
    borderRadius: 6,
    headerBackgroundColor: MUTED,
    cellPaddingHorizontal: 10,
    cellPaddingVertical: 6,
  },
  // LaTeX math (free with this engine — was V3 deferred under the walker).
  math: {
    fontSize: 16,
    color: FOREGROUND,
    backgroundColor: MUTED,
    padding: 12,
    marginBottom: 12,
    textAlign: "center" as const,
  },
  inlineMath: {
    color: FOREGROUND,
  },
};
