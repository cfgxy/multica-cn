/**
 * Mermaid render configuration for the mobile DOM component (RUYI-80).
 *
 * Pure module — no RN, no mermaid import — so the security-critical
 * invariants are unit-testable in mobile's node-only vitest lane while the
 * actual `mermaid` dependency stays inside the DOM bundle
 * (`mermaid-diagram.dom.tsx`).
 *
 * The values mirror the web renderer (`packages/views/editor/mermaid-
 * diagram.tsx`) so the same chart draws with the same rules on every
 * surface. `securityLevel: "strict"` is the XSS boundary: chart text is
 * untrusted user input, and strict mode strips scripts, event handlers and
 * HTML labels before anything is injected into the WebView's document.
 */

export interface MermaidThemeColors {
  /** Node fill — web resolves `--muted` from the host theme. */
  primaryColor: string;
  /** Node border — web resolves `--primary`. */
  primaryBorderColor: string;
  /** Label text — web resolves `--foreground`. */
  primaryTextColor: string;
  /** Edges and arrows — web resolves `--muted-foreground`. */
  lineColor: string;
  /** `"inherit"` lets the WebView document own the font stack. */
  fontFamily: string;
}

/**
 * The theme roles mobile must supply for parity with web's renderer. Kept as
 * data so the contract is assertable (see mermaid-config.test.ts).
 */
export const MERMAID_THEME_KEYS = [
  "primaryColor",
  "primaryBorderColor",
  "primaryTextColor",
  "lineColor",
] as const;

export type MermaidThemeKey = (typeof MERMAID_THEME_KEYS)[number];

export type MermaidThemeVariables = Record<MermaidThemeKey, string> & {
  fontFamily: string;
};

export function buildMermaidThemeVariables(
  colors: MermaidThemeColors,
): MermaidThemeVariables {
  return {
    primaryColor: colors.primaryColor,
    primaryBorderColor: colors.primaryBorderColor,
    primaryTextColor: colors.primaryTextColor,
    lineColor: colors.lineColor,
    fontFamily: colors.fontFamily || "inherit",
  };
}

export function buildMermaidConfig(colors: MermaidThemeColors) {
  return {
    // Render on demand — the DOM component drives `render()` itself.
    startOnLoad: false,
    // Untrusted-input boundary. Must stay "strict" — see module doc.
    securityLevel: "strict" as const,
    theme: "base" as const,
    // SVG <text> labels, not mermaid's default HTML-in-<foreignObject>.
    // Web keeps this false because foreignObject rasterizes to nothing
    // through <img> (breaking export); here it also shrinks the injection
    // surface to plain SVG text.
    htmlLabels: false,
    // Invalid syntax must reject (the component then shows its own error
    // state) instead of drawing mermaid's error graphic into the document.
    suppressErrorRendering: true,
    themeVariables: buildMermaidThemeVariables(colors),
  };
}
