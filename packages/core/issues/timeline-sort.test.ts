// @vitest-environment node

import { describe, expect, it } from "vitest";

import type { TimelineEntry } from "@multica/core/types";

import { sortTimelineEntriesForThreadedDisplay } from "./timeline-sort";

function entry(
  id: string,
  createdAt: string,
  parentId?: string,
  type: TimelineEntry["type"] = "comment",
): TimelineEntry {
  return {
    type,
    id,
    actor_type: "member",
    actor_id: "actor-1",
    created_at: createdAt,
    parent_id: parentId,
  };
}

describe("sortTimelineEntriesForThreadedDisplay", () => {
  it("orders a long-running thread after a later short thread", () => {
    const rootA = entry("root-a", "2026-09-05T04:15:24.131505Z");
    const rootB = entry("root-b", "2026-09-05T04:18:22.956452Z");
    const replyA1 = entry(
      "reply-a-1",
      "2026-09-05T04:18:22.969991Z",
      rootA.id,
    );
    const replyB = entry(
      "reply-b",
      "2026-09-05T04:20:11.507856Z",
      rootB.id,
    );
    const replyA2 = entry(
      "reply-a-2",
      "2026-09-05T16:20:55.596021Z",
      rootA.id,
    );
    const replyA3 = entry(
      "reply-a-3",
      "2026-09-05T18:07:26.079279Z",
      replyA2.id,
    );
    const replyA4 = entry(
      "reply-a-4",
      "2026-09-05T20:30:08.943283Z",
      replyA3.id,
    );
    const replyA5 = entry(
      "reply-a-5",
      "2026-09-05T20:32:02.540258Z",
      replyA3.id,
    );
    const replyA6 = entry(
      "reply-a-6",
      "2026-09-05T20:42:08.086690Z",
      replyA5.id,
    );

    const sorted = sortTimelineEntriesForThreadedDisplay([
      rootA,
      rootB,
      replyA1,
      replyB,
      replyA2,
      replyA3,
      replyA4,
      replyA5,
      replyA6,
    ]);

    expect(sorted.map((item) => item.id)).toEqual([
      "root-b",
      "reply-b",
      "root-a",
      "reply-a-1",
      "reply-a-2",
      "reply-a-3",
      "reply-a-4",
      "reply-a-5",
      "reply-a-6",
    ]);
  });

  it("ranks interleaved threads by their latest comment", () => {
    const rootA = entry("root-a", "2026-09-05T04:15:24Z");
    const rootB = entry("root-b", "2026-09-05T04:18:22Z");
    const replyB = entry("reply-b", "2026-09-05T04:20:11Z", rootB.id);
    const replyA = entry("reply-a", "2026-09-05T20:42:08Z", rootA.id);

    const sorted = sortTimelineEntriesForThreadedDisplay([
      rootA,
      rootB,
      replyB,
      replyA,
    ]);

    expect(sorted.map((item) => item.id)).toEqual([
      "root-b",
      "reply-b",
      "root-a",
      "reply-a",
    ]);
  });

  it("keeps activities positioned by their own timestamp", () => {
    const root = entry("root", "2026-09-05T04:15:24Z");
    const activity = entry(
      "activity",
      "2026-09-05T04:19:00Z",
      undefined,
      "activity",
    );
    const reply = entry("reply", "2026-09-05T04:20:11Z", root.id);

    const sorted = sortTimelineEntriesForThreadedDisplay([root, activity, reply]);

    expect(sorted.map((item) => item.id)).toEqual(["activity", "root", "reply"]);
  });

  it("promotes an orphan reply to its own display block", () => {
    const orphan = entry("orphan", "2026-09-05T04:18:22Z", "missing-parent");
    const root = entry("root", "2026-09-05T04:20:11Z");

    const sorted = sortTimelineEntriesForThreadedDisplay([root, orphan]);

    expect(sorted.map((item) => item.id)).toEqual(["orphan", "root"]);
  });
});
