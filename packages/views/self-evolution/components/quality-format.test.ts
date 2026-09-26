// @vitest-environment node

import { describe, it, expect } from "vitest";
import { formatMeasureDelta, formatMeasureValue } from "./quality-format";

/**
 * The canonical unit matrix for the seven cards (RUYI-184 返工).
 *
 * This file exists because the unit used to be inferred from a hand-kept set of
 * dimension names living here, and D2 — a 0..100 deduction score — was in the
 * set meant for 0..1 ratios, so a median of 90 printed as 9,000%. The unit now
 * arrives with the measure (`promptquality.Unit`), and what is asserted below is
 * that each unit prints as itself and that an unrecognised one degrades to a
 * plain number rather than being multiplied by a hundred.
 *
 * The mount-level assertions in `quality-tab.test.tsx` cover only what a render
 * can show; the matrix is here.
 */

describe("formatMeasureValue", () => {
  it("prints a ratio as a percentage", () => {
    expect(formatMeasureValue("ratio", 0.25, "en", 0)).toBe("25%");
  });

  // The regression: 90 points formatted as a ratio became "9,000%".
  it("prints a discipline score as points out of its maximum", () => {
    expect(formatMeasureValue("score", 90, "en", 100)).toBe("90 / 100");
    expect(formatMeasureValue("score", 100, "en", 100)).toBe("100 / 100");
  });

  it("prints a score without a maximum as a bare number", () => {
    expect(formatMeasureValue("score", 90, "en", 0)).toBe("90");
  });

  it("prints a count as a plain number", () => {
    expect(formatMeasureValue("count", 12000, "en", 0)).toBe("12,000");
    expect(formatMeasureValue("count", 2, "en", 0)).toBe("2");
  });

  // A unit a newer backend adds must not be read as a ratio: the wrong plain
  // number is a smaller error than a number multiplied by a hundred.
  it("prints an unknown unit as a plain number", () => {
    expect(formatMeasureValue("permille_v2", 90, "en", 0)).toBe("90");
  });
});

describe("formatMeasureDelta", () => {
  it("prints a ratio delta as a signed percentage", () => {
    expect(formatMeasureDelta("ratio", 0.2, 0.35, "en")).toBe("+15%");
  });

  it("prints a score delta in points, never as a percentage", () => {
    expect(formatMeasureDelta("score", 90, 96, "en")).toBe("+6");
  });

  it("prints a count delta as a signed number", () => {
    expect(formatMeasureDelta("count", 12000, 9000, "en")).toBe("-3,000");
  });

  // An absent side is not a zero: subtracting from it would manufacture a trend
  // out of one data point.
  it("returns null when either side is unmeasured", () => {
    expect(formatMeasureDelta("ratio", null, 0.35, "en")).toBeNull();
    expect(formatMeasureDelta("ratio", 0.2, null, "en")).toBeNull();
  });
});
