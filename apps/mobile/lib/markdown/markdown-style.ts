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
  // NSAttributedString / Spannable attributes (no RN nested-Text bugs).
  //
  // Calibrated against GitHub Primer's `bgColor-neutral-muted` palette
  // but at LOWER alpha than the web token (12% instead of 20%). Reason:
  // enriched-markdown paints inline `backgroundColor` over the full
  // NSAttributedString line height (Cocoa's default), and with our
  // CJK-friendly paragraph leading (lineHeight 24 on fontSize 14, ratio
  // 1.71) the painted rect ends up ~6pt taller than the glyphs — at
  // GitHub web's 20% alpha the chip reads as a heavy block. Web hides
  // this with `padding .2em .4em` + `border-radius 6px` + 85% font size;
  // enriched supports none of those for inline code, so the only knob
  // left is alpha. 12% lands close to GitHub iOS / Linear iOS levels.
  //
  // No `fontSize` override → inherits the paragraph 14pt. Web GitHub
  // uses 0.85em, but at 14pt that drops to ~12pt which hurts mobile
  // legibility; Linear iOS, Notion mobile, and GitHub iOS all keep
  // inline code at body size for the same reason.
  //
  // No `borderColor` — the API supports it but has no `borderWidth`,
  // and a translucent fill with a hairline border reads grubby. GitHub,
  // Slack, Linear, Notion all skip the border on inline code.
  // (`padding` / `borderRadius` aren't supported on inline code at all
  // in enriched-markdown.)
  code: {
    color: FOREGROUND,
    backgroundColor: "#afb8c11f",
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
