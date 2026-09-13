// @vitest-environment node

import { describe, expect, it } from "vitest";

import type { TimelineEntry } from "@multica/core/types";

import { buildTimelineModel, latestThreadComment } from "./timeline-sort";

function entry(
  id: string,
  createdAt: string,
  parentId?: string,
  type: TimelineEntry["type"] = "comment",
  action?: string,
): TimelineEntry {
  return {
    type,
    id,
    actor_type: "member",
    actor_id: "actor-1",
    created_at: createdAt,
    parent_id: parentId,
    action,
  };
}

describe("buildTimelineModel", () => {
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

    const sorted = buildTimelineModel([
      rootA,
      rootB,
      replyA1,
      replyB,
      replyA2,
      replyA3,
      replyA4,
      replyA5,
      replyA6,
    ], "recent-comment");

    expect(sorted.entries.map((item) => item.id)).toEqual([
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

    const sorted = buildTimelineModel([
      rootA,
      rootB,
      replyB,
      replyA,
    ], "recent-comment");

    expect(sorted.entries.map((item) => item.id)).toEqual([
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

    const sorted = buildTimelineModel([root, activity, reply], "recent-comment");

    expect(sorted.entries.map((item) => item.id)).toEqual(["activity", "root", "reply"]);
  });

  it("promotes an orphan reply to its own display block", () => {
    const orphan = entry("orphan", "2026-09-05T04:18:22Z", "missing-parent");
    const root = entry("root", "2026-09-05T04:20:11Z");

    const sorted = buildTimelineModel([root, orphan], "recent-comment");

    expect(sorted.entries.map((item) => item.id)).toEqual(["orphan", "root"]);
  });

  it("uses thread creation time without changing reply order in created mode", () => {
    const rootA = entry("root-a", "2026-09-05T09:00:00Z");
    const rootB = entry("root-b", "2026-09-05T10:00:00Z");
    const replyA = entry("reply-a", "2026-09-05T11:00:00Z", rootA.id);

    const recent = buildTimelineModel([rootA, rootB, replyA], "recent-comment");
    const created = buildTimelineModel([rootA, rootB, replyA], "created");

    expect(recent.entries.map((item) => item.id)).toEqual(["root-b", "root-a", "reply-a"]);
    expect(created.entries.map((item) => item.id)).toEqual(["root-a", "reply-a", "root-b"]);
  });

  it("changes only top-level thread placement while preserving activity and reply order", () => {
    const rootA = entry("root-a", "2026-09-05T09:00:00Z");
    const activityA = entry(
      "activity-a",
      "2026-09-05T09:30:00Z",
      undefined,
      "activity",
      "status_changed",
    );
    const rootB = entry("root-b", "2026-09-05T10:00:00Z");
    const replyB = entry("reply-b", "2026-09-05T10:30:00Z", rootB.id);
    const activityB = entry(
      "activity-b",
      "2026-09-05T11:00:00Z",
      undefined,
      "activity",
      "priority_changed",
    );
    const replyA = entry("reply-a", "2026-09-05T12:00:00Z", rootA.id);
    const raw = [activityB, replyA, rootB, activityA, rootA, replyB];

    expect(buildTimelineModel(raw, "recent-comment").entries.map((item) => item.id)).toEqual([
      "activity-a",
      "root-b",
      "reply-b",
      "activity-b",
      "root-a",
      "reply-a",
    ]);
    expect(buildTimelineModel(raw, "created").entries.map((item) => item.id)).toEqual([
      "root-a",
      "reply-a",
      "activity-a",
      "root-b",
      "reply-b",
      "activity-b",
    ]);
  });

  it("uses ids to break equal timestamps for blocks and replies", () => {
    const rootB = entry("root-b", "2026-09-05T09:00:00Z");
    const rootA = entry("root-a", "2026-09-05T09:00:00Z");
    const replyZ = entry("reply-z", "2026-09-05T10:00:00Z", rootA.id);
    const replyA = entry("reply-a", "2026-09-05T10:00:00Z", rootA.id);

    expect(
      buildTimelineModel([replyZ, rootB, replyA, rootA], "created").entries.map(
        (item) => item.id,
      ),
    ).toEqual(["root-a", "reply-a", "reply-z", "root-b"]);
  });

  it.each(["task_completed", "task_failed"])(
    "coalesces %s activity regardless of elapsed time",
    (action) => {
      const first = entry(
        `${action}-1`,
        "2026-09-05T09:00:00Z",
        undefined,
        "activity",
        action,
      );
      const second = entry(
        `${action}-2`,
        "2026-09-06T09:00:00Z",
        undefined,
        "activity",
        action,
      );

      expect(buildTimelineModel([first, second], "created").entries).toEqual([
        expect.objectContaining({ id: `${action}-2`, coalesced_count: 2 }),
      ]);
    },
  );

  it("never coalesces squad leader evaluations", () => {
    const first = entry(
      "evaluation-1",
      "2026-09-05T09:00:00Z",
      undefined,
      "activity",
      "squad_leader_evaluated",
    );
    const second = entry(
      "evaluation-2",
      "2026-09-05T09:01:00Z",
      undefined,
      "activity",
      "squad_leader_evaluated",
    );

    expect(
      buildTimelineModel([second, first], "recent-comment").entries.map(
        (item) => item.id,
      ),
    ).toEqual(["evaluation-1", "evaluation-2"]);
  });

  it("coalesces activities only after sorting display blocks and reports stable stats", () => {
    const root = entry("root", "2026-09-05T09:00:00Z");
    const reply = entry("reply", "2026-09-05T10:00:00Z", root.id);
    const first = entry("activity-1", "2026-09-05T09:10:00Z", undefined, "activity", "status_changed");
    const second = entry("activity-2", "2026-09-05T09:11:00Z", undefined, "activity", "status_changed");

    const model = buildTimelineModel([root, first, reply, second], "recent-comment");

    expect(model.entries.map((item) => item.id)).toEqual(["activity-2", "root", "reply"]);
    expect(model.entries[0]?.coalesced_count).toBe(2);
    expect(model.stats).toEqual({
      threadBlockCount: 1,
      activityEntryCount: 2,
      activityVisualGroupCount: 1,
      sortableBlockCount: 3,
    });
  });
});

describe("latestThreadComment", () => {
  it("selects the last reply with the stable timestamp and id tie-breaker", () => {
    const root = entry("root", "2026-09-05T09:00:00Z");
    const earlierReply = entry("reply-a", "2026-09-05T10:00:00Z", root.id);
    const laterSameTimeReply = entry("reply-z", "2026-09-05T10:00:00Z", root.id);

    expect(latestThreadComment([laterSameTimeReply, root, earlierReply])).toBe(
      laterSameTimeReply,
    );
  });
});
