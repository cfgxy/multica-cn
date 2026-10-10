import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { addDaysIso, todayIso, weekStartIso } from "./date-utils";

describe("weekStartIso", () => {
  it("returns the Monday of the same ISO week", () => {
    // 2026-05-19 is a Tuesday → Monday is 2026-05-18.
    expect(weekStartIso("2026-05-19")).toBe("2026-05-18");
  });

  it("treats Monday as the start of its own week (idempotent)", () => {
    expect(weekStartIso("2026-05-18")).toBe("2026-05-18");
  });

  it("rolls Sunday back to the previous Monday", () => {
    // 2026-05-17 is a Sunday → Monday is 2026-05-11.
    expect(weekStartIso("2026-05-17")).toBe("2026-05-11");
  });

  it("crosses month and year boundaries", () => {
    // 2026-01-03 is a Saturday → Monday is 2025-12-29.
    expect(weekStartIso("2026-01-03")).toBe("2025-12-29");
  });
});

describe("addDaysIso", () => {
  it("adds across month boundary", () => {
    expect(addDaysIso("2026-05-30", 3)).toBe("2026-06-02");
  });

  it("subtracts across year boundary", () => {
    expect(addDaysIso("2026-01-02", -5)).toBe("2025-12-28");
  });
});

describe("todayIso", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("uses the runtime's timezone, not the host's, to decide today", () => {
    // 2026-05-19 16:00 UTC. In Asia/Shanghai (UTC+8) it's already 2026-05-20.
    // In America/Los_Angeles (UTC-7 on this date) it's still 2026-05-19.
    vi.setSystemTime(new Date("2026-05-19T16:00:00Z"));
    expect(todayIso("Asia/Shanghai")).toBe("2026-05-20");
    expect(todayIso("America/Los_Angeles")).toBe("2026-05-19");
    expect(todayIso("UTC")).toBe("2026-05-19");
  });
});
