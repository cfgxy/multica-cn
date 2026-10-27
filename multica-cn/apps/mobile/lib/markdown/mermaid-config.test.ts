import { describe, expect, it } from "vitest";
import {
  MERMAID_THEME_KEYS,
  buildMermaidConfig,
  type MermaidThemeColors,
} from "./mermaid-config";

const COLORS: MermaidThemeColors = {
  primaryColor: "hsl(0 0% 96.1%)",
  primaryBorderColor: "hsl(0 0% 9%)",
  primaryTextColor: "hsl(0 0% 3.9%)",
  lineColor: "hsl(0 0% 45.1%)",
  fontFamily: "inherit",
};

describe("buildMermaidConfig (RUYI-80)", () => {
  it("renders labels as SVG text — HTML labels would paint nothing and are the XSS surface", () => {
    expect(buildMermaidConfig(COLORS).htmlLabels).toBe(false);
  });

  it("keeps securityLevel strict — chart text is untrusted input", () => {
    // Red line (RUYI-80 dispatch card): mermaid supports inline HTML in
    // labels; `strict` strips event handlers, scripts and remote loads from
    // the diagram source before anything reaches the DOM.
    expect(buildMermaidConfig(COLORS).securityLevel).toBe("strict");
  });

  it("throws instead of painting mermaid's built-in error graphic", () => {
    expect(buildMermaidConfig(COLORS).suppressErrorRendering).toBe(true);
    expect(buildMermaidConfig(COLORS).startOnLoad).toBe(false);
  });

  it("maps the host theme tokens onto mermaid's theme variables", () => {
    const config = buildMermaidConfig(COLORS);

    expect(config.theme).toBe("base");
    expect(config.themeVariables).toEqual({
      ...COLORS,
      fontFamily: "inherit",
    });
  });

  it("carries every theme key the web renderer consumes", () => {
    // Parity guard: packages/views/editor/mermaid-diagram.tsx resolves the
    // same four color roles from CSS variables. If web grows a role, mobile
    // should follow — this list is the contract between the two sides.
    expect(MERMAID_THEME_KEYS).toEqual([
      "primaryColor",
      "primaryBorderColor",
      "primaryTextColor",
      "lineColor",
    ]);
  });
});
