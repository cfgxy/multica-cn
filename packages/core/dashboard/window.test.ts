import { describe, expect, it } from "vitest";

import {
  canShiftNext,
  dimsForWindowLength,
  isCurrentWindow,
  quickWindow,
  shiftWindow,
  shiftedWindow,
  windowLength,
} from "./window";

describe("quickWindow", () => {
  it("1d is the viewer's natural today, not the last 24 hours", () => {
    expect(quickWindow(1, "2026-03-10")).toEqual({
      start: "2026-03-10",
      end: "2026-03-10",
    });
  });

  it("7d covers the seven natural days ending today", () => {
    expect(quickWindow(7, "2026-03-10")).toEqual({
      start: "2026-03-04",
      end: "2026-03-10",
    });
  });

  it("30d crosses month boundaries on string math", () => {
    expect(quickWindow(30, "2026-03-10")).toEqual({
      start: "2026-02-09",
      end: "2026-03-10",
    });
  });
});

describe("shiftedWindow", () => {
  it("1d offset 1 is exactly yesterday", () => {
    expect(shiftedWindow(1, 1, "2026-03-10")).toEqual({
      start: "2026-03-09",
      end: "2026-03-09",
    });
  });

  it("1d offset 2 is the day before yesterday", () => {
    expect(shiftedWindow(1, 2, "2026-03-10")).toEqual({
      start: "2026-03-08",
      end: "2026-03-08",
    });
  });

  it("7d offset 1 is the whole previous window, not a one-day slide", () => {
    expect(shiftedWindow(7, 1, "2026-03-10")).toEqual({
      start: "2026-02-25",
      end: "2026-03-03",
    });
  });

  it("offset 0 is the current window", () => {
    expect(shiftedWindow(30, 0, "2026-03-10")).toEqual(quickWindow(30, "2026-03-10"));
  });
});

describe("windowLength", () => {
  it("counts both ends inclusively", () => {
    expect(windowLength({ start: "2026-03-10", end: "2026-03-10" })).toBe(1);
    expect(windowLength({ start: "2026-03-04", end: "2026-03-10" })).toBe(7);
  });

  it("survives month and year boundaries", () => {
    expect(windowLength({ start: "2026-02-25", end: "2026-03-03" })).toBe(7);
    expect(windowLength({ start: "2025-12-30", end: "2026-01-02" })).toBe(4);
  });
});

describe("shiftWindow", () => {
  it("moves a custom window by its own length", () => {
    const w = { start: "2026-02-20", end: "2026-02-26" };
    expect(shiftWindow(w, -1)).toEqual({ start: "2026-02-13", end: "2026-02-19" });
    expect(shiftWindow(w, 1)).toEqual({ start: "2026-02-27", end: "2026-03-05" });
  });

  it("moves single-day windows one day at a time", () => {
    const w = { start: "2026-03-01", end: "2026-03-01" };
    expect(shiftWindow(w, -1)).toEqual({ start: "2026-02-28", end: "2026-02-28" });
  });
});

describe("canShiftNext", () => {
  const today = "2026-03-10";

  it("rejects the move that would land the window in the future", () => {
    expect(canShiftNext({ start: "2026-03-04", end: "2026-03-10" }, today)).toBe(false);
    expect(canShiftNext({ start: "2026-03-10", end: "2026-03-10" }, today)).toBe(false);
  });

  it("allows the move when the next window still ends today or earlier", () => {
    // One period forward from offset-1 lands exactly on the current window —
    // that is how "next" walks back to the present without stepping past it.
    expect(canShiftNext({ start: "2026-02-25", end: "2026-03-03" }, today)).toBe(true);
    expect(canShiftNext({ start: "2026-03-09", end: "2026-03-09" }, today)).toBe(true);
    expect(canShiftNext({ start: "2026-02-01", end: "2026-02-09" }, today)).toBe(true);
  });
});

describe("isCurrentWindow", () => {
  it("reads the end date, not the start", () => {
    expect(isCurrentWindow({ start: "2026-03-04", end: "2026-03-10" }, "2026-03-10")).toBe(true);
    expect(isCurrentWindow({ start: "2026-02-25", end: "2026-03-03" }, "2026-03-10")).toBe(false);
  });
});

describe("dimsForWindowLength", () => {
  it("follows the same ladder as the quick ranges", () => {
    expect(dimsForWindowLength(1)).toEqual(["daily"]);
    expect(dimsForWindowLength(7)).toEqual(["daily"]);
    expect(dimsForWindowLength(8)).toEqual(["daily", "weekly"]);
    expect(dimsForWindowLength(30)).toEqual(["daily", "weekly"]);
    expect(dimsForWindowLength(91)).toEqual(["weekly"]);
    expect(dimsForWindowLength(200)).toEqual(["weekly"]);
  });
});
