// @vitest-environment node

import { describe, expect, it } from "vitest";

import type { TimelineEntry } from "@multica/core/types";

import { buildTimelineRows, buildTimelineRowsModel } from "./timeline-thread";

function comment(id: string, createdAt: string, parentId?: string): TimelineEntry {
  return {
    type: "comment",
    id,
    actor_type: "agent",
    actor_id: "agent-1",
    created_at: createdAt,
    parent_id: parentId,
  };
}

describe("buildTimelineRows", () => {
  it("orders interleaved thread rows by each thread's latest reply", () => {
    const rootA = comment("root-a", "2026-09-05T04:15:24Z");
    const rootB = comment("root-b", "2026-09-05T04:18:22Z");
    const replyB = comment("reply-b", "2026-09-05T04:20:11Z", rootB.id);
    const replyA = comment("reply-a", "2026-09-05T20:42:08Z", rootA.id);

    const rows = buildTimelineRows([rootA, rootB, replyB, replyA]);

    expect(rows.map((row) => row.entry.id)).toEqual(["root-b", "root-a"]);
    expect(rows[0]?.replies.map((reply) => reply.id)).toEqual(["reply-b"]);
    expect(rows[1]?.replies.map((reply) => reply.id)).toEqual(["reply-a"]);
  });

  it("returns the Core model statistics used to decide whether sorting can change", () => {
    const root = comment("root", "2026-09-05T04:15:24Z");
    const reply = comment("reply", "2026-09-05T04:20:11Z", root.id);

    const model = buildTimelineRowsModel([root, reply], "created");

    expect(model.rows.map((row) => row.entry.id)).toEqual(["root"]);
    expect(model.stats).toMatchObject({
      threadBlockCount: 1,
      sortableBlockCount: 1,
    });
  });
});
