/**
 * `markdownStyle` prop value for `EnrichedMarkdownText`. Driven by RNR
 * theme tokens (`apps/mobile/lib/theme.ts`, mirroring CSS variables in
 * `apps/mobile/global.css`) so colors track light/dark automatically.
 *
 * Why a hook instead of a static object: enriched-markdown is a native
 * (md4c → NSAttributedString / Spannable) layer that only accepts an
 * imperative style object — it can NOT consume NativeWind classNames.
 * The hook is the bridge: it reads the current colorScheme via the same
 * `useColorScheme` everything else in the app uses, and rebuilds the
 * style object whenever the theme flips.
 *
 * Sizing follows the mobile typography scale documented in
 * `apps/mobile/docs/markdown-renderer-research.md` → "Mobile typography
 * scale" (calibrated against Apple HIG; one tier below shadcn web defaults
 * because markdown headings inside an issue card are structural, not
 * screen titles). HIG values are encoded in `MD_FONT` / `MD_LINE` /
 * `MD_GAP` constants — these are NOT RNR tokens to replace; they are
 * mobile-specific design constants validated by the 2026-05-09 inline-
 * code incident.
 */
import { useMemo } from "react";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";

/**
 * Typography scale — Apple HIG-calibrated, one tier below shadcn web.
 * See `docs/markdown-renderer-research.md` "Mobile typography scale".
 */
const MD_FONT = {
  body: 14,
  h1: 20,
  h2: 18,
  h3: 16,
  h4: 14,
  h5: 14,
  h6: 12,
  codeBlock: 13,
} as const;

const MD_LINE = {
  body: 24, // text-sm + leading-6 ≈ 1.71, generous for CJK
} as const;

const MD_GAP = {
  paragraph: 12,
  headingTopLarge: 16,
  headingTopSmall: 12,
  headingBottomLarge: 8,
  headingBottomSmall: 6,
} as const;

/**
 * Inline code background — translucent neutral overlay computed against
 * the current `foreground` so it reads consistently in light AND dark.
 * Web's chip uses a 20% alpha; mobile uses 12% because enriched paints
 * inline `backgroundColor` over the full NSAttributedString line height
 * (Cocoa default) and our CJK-friendly leading (24 on 14 = 1.71) makes
 * the painted rect ~6pt taller than the glyphs. At 20% alpha the chip
 * reads as a heavy block; 12% lands close to GitHub iOS / Linear iOS.
 *
 * We construct via hex+alpha suffix instead of reading code-surface
 * because:
 *   1. code-surface is a SOLID color for fenced code blocks — semantically
 *      different from the translucent inline overlay.
 *   2. The translucency lets inline code work over any background
 *      (paragraph bg, blockquote bg, future colored callouts) without
 *      needing per-context tuning.
 */
function inlineCodeBg(scheme: "light" | "dark"): string {
  // Neutral mid-gray with 12% alpha. Tracks scheme: a slightly lighter
  // shade in dark mode so the overlay is visible against deep backgrounds.
  return scheme === "dark" ? "#9ca3af1f" : "#afb8c11f";
}

export function useMarkdownStyle() {
  const { isDarkColorScheme } = useColorScheme();
  const scheme = isDarkColorScheme ? "dark" : "light";
  const t = isDarkColorScheme ? THEME.dark : THEME.light;

  return useMemo(
    () => ({
      // Body / paragraph — text-sm + leading-6 ≈ 1.71. Generous for CJK.
      paragraph: {
        fontSize: MD_FONT.body,
        lineHeight: MD_LINE.body,
        color: t.foreground,
        marginBottom: MD_GAP.paragraph,
      },
      // Headings — Apple HIG-calibrated, one tier below shadcn web defaults.
      h1: {
        fontSize: MD_FONT.h1,
        fontWeight: "700" as const,
        color: t.foreground,
        marginTop: MD_GAP.headingTopLarge,
        marginBottom: MD_GAP.headingBottomLarge,
      },
      h2: {
        fontSize: MD_FONT.h2,
        fontWeight: "600" as const,
        color: t.foreground,
        marginTop: MD_GAP.headingTopLarge,
        marginBottom: MD_GAP.headingBottomLarge,
      },
      h3: {
        fontSize: MD_FONT.h3,
        fontWeight: "600" as const,
        color: t.foreground,
        marginTop: MD_GAP.headingTopSmall,
        marginBottom: MD_GAP.headingBottomSmall,
      },
      h4: {
        fontSize: MD_FONT.h4,
        fontWeight: "600" as const,
        color: t.foreground,
        marginTop: MD_GAP.headingTopSmall,
        marginBottom: MD_GAP.headingBottomSmall,
      },
      h5: {
        fontSize: MD_FONT.h5,
        fontWeight: "600" as const,
        color: t.foreground,
        marginTop: MD_GAP.headingTopSmall,
        marginBottom: MD_GAP.headingBottomSmall,
      },
      h6: {
        fontSize: MD_FONT.h6,
        fontWeight: "600" as const,
        color: t.mutedForeground,
        marginTop: MD_GAP.headingTopSmall,
        marginBottom: MD_GAP.headingBottomSmall,
      },
      strong: {
        // md4c restricts inline `fontWeight` to "bold" | "normal" — it adds
        // the bold trait on top of the inherited block font. We can't pin
        // a 600 weight here the way we can on headings.
        fontWeight: "bold" as const,
      },
      link: {
        color: t.brand,
        underline: true,
      },
      // Inline code — bg + monospace. md4c renders this natively into
      // NSAttributedString / Spannable attributes (no RN nested-Text bugs).
      // No fontSize / border / padding overrides — see `inlineCodeBg`
      // docstring for the alpha rationale and the 2026-05-09 incident.
      code: {
        color: t.foreground,
        backgroundColor: inlineCodeBg(scheme),
      },
      // Block code — bigger box, muted background, mono font. (When the
      // splitter detects a fenced code block it routes to the in-house
      // `CodeBlock` component instead — this style is the fallback for
      // any code that stays inside the enriched prose stream.)
      codeBlock: {
        fontSize: MD_FONT.codeBlock,
        color: t.foreground,
        backgroundColor: t.muted,
        padding: 12,
        borderRadius: 8,
        marginBottom: MD_GAP.paragraph,
      },
      // Blockquote — subtle left bar in border tone.
      blockquote: {
        borderColor: t.border,
        borderWidth: 2,
        backgroundColor: "transparent",
        marginBottom: MD_GAP.paragraph,
      },
      // List — bullets in muted-foreground so they don't compete with content.
      list: {
        fontSize: MD_FONT.body,
        bulletColor: t.mutedForeground,
        bulletSize: 4,
        markerColor: t.mutedForeground,
        gapWidth: 8,
        marginLeft: 16,
      },
      image: {
        borderRadius: 8,
        marginBottom: MD_GAP.paragraph,
      },
      taskList: {
        checkedColor: t.brand,
        borderColor: t.border,
        checkmarkColor: t.brandForeground,
        checkboxSize: 16,
      },
      // GFM tables.
      table: {
        fontSize: MD_FONT.body,
        borderColor: t.border,
        borderRadius: 6,
        headerBackgroundColor: t.muted,
        cellPaddingHorizontal: 10,
        cellPaddingVertical: 6,
      },
      // LaTeX math (free with this engine — was V3 deferred under the walker).
      math: {
        fontSize: 16,
        color: t.foreground,
        backgroundColor: t.muted,
        padding: 12,
        marginBottom: MD_GAP.paragraph,
        textAlign: "center" as const,
      },
      inlineMath: {
        color: t.foreground,
      },
    }),
    [t, scheme],
  );
}
