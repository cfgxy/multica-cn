// @vitest-environment node

import { describe, expect, it } from "vitest";

import type { TimelineEntry } from "@multica/core/types";

import {
  crossedTimelineHardCap,
  effectiveTruncatedKinds,
  parseTimelineTruncatedKinds,
  updateTimelineEntries,
  type TimelineQueryData,
} from "./timeline-query";

function entry(id: string, type: TimelineEntry["type"]): TimelineEntry {
  return {
    id,
    type,
    actor_type: "member",
    actor_id: "actor-1",
    created_at: "2026-09-05T09:00:00Z",
  };
}

describe("parseTimelineTruncatedKinds", () => {
  it("normalizes known header values and ignores unknown values", () => {
    expect(parseTimelineTruncatedKinds(" activity, comment, activity, future ")).toEqual([
      "activity",
      "comment",
    ]);
    expect(parseTimelineTruncatedKinds(null)).toEqual([]);
  });
});

describe("updateTimelineEntries", () => {
  it("does not materialize an incomplete cache", () => {
    let called = false;

    expect(updateTimelineEntries(undefined, () => {
      called = true;
      return [];
    })).toBeUndefined();
    expect(called).toBe(false);
  });

  it("preserves the full-fetch truncation snapshot", () => {
    const previous: TimelineQueryData = {
      entries: [entry("comment-1", "comment")],
      truncatedKinds: ["activity"],
    };

    expect(updateTimelineEntries(previous, (entries) => [...entries, entry("comment-2", "comment")])).toEqual({
      entries: [entry("comment-1", "comment"), entry("comment-2", "comment")],
      truncatedKinds: ["activity"],
    });
  });
});

describe("crossedTimelineHardCap", () => {
  it("fires only when the same entry kind crosses from 2000 to 2001", () => {
    const atCap = Array.from({ length: 2000 }, (_, index) =>
      entry(`comment-${index}`, "comment"),
    );
    const overCap = [...atCap, entry("comment-2000", "comment")];

    expect(crossedTimelineHardCap(atCap, overCap, "comment")).toBe(true);
    expect(crossedTimelineHardCap(atCap.slice(1), atCap, "comment")).toBe(false);
    expect(crossedTimelineHardCap(atCap, [...atCap, entry("activity-1", "activity")], "activity")).toBe(false);
  });
});

describe("effectiveTruncatedKinds", () => {
  it("reports a kind immediately when a cache crosses from 2000 to 2001 entries", () => {
    const data: TimelineQueryData = {
      entries: Array.from({ length: 2001 }, (_, index) => entry(`comment-${index}`, "comment")),
      truncatedKinds: [],
    };

    expect(effectiveTruncatedKinds(data)).toEqual(["comment"]);
  });
});
