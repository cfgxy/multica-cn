import { describe, expect, it } from "vitest";

import { formatLastActive, squadMemberStatusPill } from "./squad-status-pill";

describe("squadMemberStatusPill", () => {
  it("maps the five server-derived buckets to key/fallback/tone", () => {
    expect(squadMemberStatusPill("working")).toEqual({
      key: "members_tab.status_working",
      fallback: "Working",
      tone: "success",
    });
    expect(squadMemberStatusPill("idle")).toEqual({
      key: "members_tab.status_idle",
      fallback: "Idle",
      tone: "brand",
    });
    expect(squadMemberStatusPill("offline")).toEqual({
      key: "members_tab.status_offline",
      fallback: "Offline",
      tone: "muted",
    });
    expect(squadMemberStatusPill("unstable")).toEqual({
      key: "members_tab.status_unstable",
      fallback: "Unstable",
      tone: "warning",
    });
    expect(squadMemberStatusPill("archived")).toEqual({
      key: "members_tab.status_archived",
      fallback: "Archived",
      tone: "muted",
    });
  });

  it("returns null for humans (status is null/undefined)", () => {
    expect(squadMemberStatusPill(null)).toBeNull();
    expect(squadMemberStatusPill(undefined)).toBeNull();
  });

  it("returns null for unknown future values (neutral fallback)", () => {
    expect(squadMemberStatusPill("escalated" as never)).toBeNull();
  });
});

describe("formatLastActive", () => {
  it("returns null when the member was never active", () => {
    expect(formatLastActive(null)).toBeNull();
    expect(formatLastActive(undefined)).toBeNull();
    expect(formatLastActive("")).toBeNull();
  });

  it("returns null for unparseable timestamps", () => {
    expect(formatLastActive("not-a-date")).toBeNull();
  });

  it("formats a parseable timestamp as a short absolute stamp", () => {
    const value = "2026-10-02T10:30:00Z";
    const expected = new Intl.DateTimeFormat(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }).format(new Date(value));
    expect(formatLastActive(value)).toBe(expected);
    expect(formatLastActive(value)).toBeTruthy();
  });
});
