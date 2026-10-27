// @vitest-environment node
import { describe, expect, it } from "vitest";
import { shouldWrapTaskPills } from "./task-toolbar";

/**
 * Tasks toolbar pill-row layout decision (RUYI-344 item 10, P2): at 1.3x
 * fontScale the five pills overflow the single row, the horizontal drag
 * proved dead on device (zero displacement), and clipped-off TABs became
 * unreachable. Above fontScale 1 the row must switch to an adaptive
 * wrapping flow; at 1.0 the QA-accepted single-row scroller stays.
 */
describe("task toolbar pill wrap decision", () => {
  it("keeps the single-row scroller at fontScale 1 (QA-accepted rendering)", () => {
    expect(shouldWrapTaskPills(1)).toBe(false);
  });

  it("wraps pills above fontScale 1 so every TAB is reachable without a gesture", () => {
    expect(shouldWrapTaskPills(1.05)).toBe(true);
    expect(shouldWrapTaskPills(1.3)).toBe(true);
    expect(shouldWrapTaskPills(2)).toBe(true);
  });
});
