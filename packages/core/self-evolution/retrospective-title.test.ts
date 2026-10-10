import { describe, expect, it } from "vitest";
import { formatRetrospectiveStamp } from "./retrospective-title";

// Fixed instants pinned to UTC; the stamp itself renders in the viewer's
// local timezone, so each case asserts via the same-zone expectation.
describe("formatRetrospectiveStamp", () => {
  it("renders YYYYMMDD_HHmmss in the local timezone", () => {
    // 2026-10-11 08:09:10 UTC expressed in local time via the Date ctor's
    // component-wise builder keeps the assertion timezone-independent.
    const d = new Date(2026, 9, 11, 8, 9, 10);
    expect(formatRetrospectiveStamp(d)).toBe("20261011_080910");
  });

  it("zero-pads single-digit fields", () => {
    const d = new Date(2026, 0, 2, 3, 4, 5);
    expect(formatRetrospectiveStamp(d)).toBe("20260102_030405");
  });

  it("accepts an RFC3339 string (the server payload shape)", () => {
    // Round-trip via toISOString: parsing the RFC3339 form must land on the
    // same instant (and therefore the same local-time stamp) as the Date.
    const d = new Date(2026, 8, 30, 23, 59, 0);
    expect(formatRetrospectiveStamp(d.toISOString())).toBe(
      formatRetrospectiveStamp(d),
    );
    expect(formatRetrospectiveStamp(d.toISOString())).toMatch(
      /^\d{8}_\d{6}$/,
    );
  });

  it("returns an empty string for unparseable input so callers can fall back", () => {
    expect(formatRetrospectiveStamp("not-a-date")).toBe("");
  });
});
