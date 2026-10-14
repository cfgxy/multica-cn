// @vitest-environment node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { withAlphaColor } from "./color-alpha";

const LIB_DIR = path.dirname(fileURLToPath(import.meta.url));
const MOBILE_ROOT = path.resolve(LIB_DIR, "..");

/**
 * Every quoted color token declared in lib/theme.ts. THEME strings are
 * hsl() space-syntax tokens (plus one hex fallback in plus-panel.tsx), so
 * this is the exact input space withAlphaColor must handle — asserted
 * from source rather than by importing theme.ts, which pulls in
 * @react-navigation/native and would drag the RN dependency chain into
 * this node-only lane.
 */
function themeColorTokens(): string[] {
  const source = fs.readFileSync(path.join(LIB_DIR, "theme.ts"), "utf8");
  return [...source.matchAll(/"((?:hsl|hsla|rgb|rgba|#)[^"]*)"/g)].map(
    (match) => match[1],
  );
}

const VALID_RGBA =
  /^rgba\(\d{1,3}, \d{1,3}, \d{1,3}, (?:0(?:\.\d+)?|1(?:\.0+)?)\)$/;

describe("withAlphaColor", () => {
  it("blends hsl() space-syntax tokens into a legal rgba() string", () => {
    expect(withAlphaColor("hsl(0 0% 9%)", 0.1)).toBe("rgba(23, 23, 23, 0.1)");
    expect(withAlphaColor("hsl(0 0% 98%)", 0.1)).toBe(
      "rgba(250, 250, 250, 0.1)",
    );
  });

  it("blends the brand token the + panel accent tiles are spec'd on", () => {
    expect(withAlphaColor("hsl(225 71% 58%)", 0.1)).toBe(
      "rgba(72, 110, 224, 0.1)",
    );
  });

  it("blends hex colors, including 8-digit input (its alpha is overridden)", () => {
    expect(withAlphaColor("#2563eb", 0.1)).toBe("rgba(37, 99, 235, 0.1)");
    expect(withAlphaColor("#2563eb1A", 1)).toBe("rgba(37, 99, 235, 1)");
  });

  it("keeps boundary alphas exact and clamps out-of-range ones", () => {
    expect(withAlphaColor("hsl(0 0% 0%)", 0)).toBe("rgba(0, 0, 0, 0)");
    expect(withAlphaColor("hsl(0 0% 100%)", 1)).toBe("rgba(255, 255, 255, 1)");
    expect(withAlphaColor("hsl(0 0% 0%)", 2)).toBe("rgba(0, 0, 0, 1)");
    expect(withAlphaColor("hsl(0 0% 0%)", -1)).toBe("rgba(0, 0, 0, 0)");
  });

  it("returns unparseable input unchanged instead of building an invalid color", () => {
    expect(withAlphaColor("not-a-color", 0.1)).toBe("not-a-color");
    expect(withAlphaColor("", 0.1)).toBe("");
  });

  it("resolves every THEME color token to a legal color", () => {
    const tokens = themeColorTokens();
    expect(tokens.length).toBeGreaterThan(40);
    for (const token of tokens) {
      expect(withAlphaColor(token, 0.1), token).toMatch(VALID_RGBA);
    }
  });
});

describe("plus-panel accent tile background (RUYI-741)", () => {
  const source = fs.readFileSync(
    path.join(MOBILE_ROOT, "components/chat/plus-panel.tsx"),
    "utf8",
  );

  it("no longer concatenates a hex-alpha suffix onto a theme token", () => {
    expect(source).not.toMatch(/\+\s*"1A"/);
  });

  it("builds the big-tile background via withAlphaColor(theme.brand, 0.1)", () => {
    expect(source).toMatch(/withAlphaColor\(theme\.brand,\s*0\.1\)/);
  });

  it("renders the big-tile icon in the brand color", () => {
    expect(source).toMatch(/color=\{theme\.brand\}/);
  });
});
