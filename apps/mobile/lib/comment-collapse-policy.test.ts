// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TimelineEntry } from "@multica/core/types";
import { buildTimelineRows } from "./timeline-thread";
import { computeReentryExpandedRoots } from "./comment-collapse-policy";

function comment(
  id: string,
  actorType: "agent" | "member",
  actorId: string,
  createdAt: string,
  extra: Partial<TimelineEntry> = {},
): TimelineEntry {
  return {
    type: "comment",
    id,
    actor_type: actorType,
    actor_id: actorId,
    created_at: createdAt,
    ...extra,
  };
}

function root(entry: TimelineEntry, replies: TimelineEntry[] = []) {
  return { entry, replies };
}

describe("computeReentryExpandedRoots (RUYI-78 read-state re-entry policy)", () => {
  it("returns an empty set for an empty timeline", () => {
    expect(computeReentryExpandedRoots([])).toEqual(new Set());
  });

  it("expands only an agent's chronologically-last root, collapses its earlier roots", () => {
    const rows = [
      root(comment("c1", "agent", "a1", "2026-09-01T10:00:00Z")),
      root(comment("c2", "agent", "a1", "2026-09-02T10:00:00Z")),
      root(comment("c3", "agent", "a1", "2026-09-03T10:00:00Z")),
    ];
    expect(computeReentryExpandedRoots(rows)).toEqual(new Set(["c3"]));
  });

  it("expands each agent's own last root independently", () => {
    const rows = [
      root(comment("a1-old", "agent", "agent-1", "2026-09-01T10:00:00Z")),
      root(comment("a2-old", "agent", "agent-2", "2026-09-01T11:00:00Z")),
      root(comment("a1-new", "agent", "agent-1", "2026-09-02T10:00:00Z")),
      root(comment("a2-new", "agent", "agent-2", "2026-09-02T11:00:00Z")),
    ];
    expect(computeReentryExpandedRoots(rows)).toEqual(
      new Set(["a1-new", "a2-new"]),
    );
  });

  it("expands the HOST root when an agent's latest comment is a reply", () => {
    const rows = [
      root(comment("r1", "agent", "agent-1", "2026-09-01T10:00:00Z"), [
        comment("r1-reply", "agent", "agent-1", "2026-09-03T09:00:00Z"),
      ]),
      root(comment("r2", "agent", "agent-1", "2026-09-02T10:00:00Z")),
    ];
    // r2 is newer than r1's root comment but OLDER than agent-1's latest
    // (the reply inside r1) — so r1 must win.
    expect(computeReentryExpandedRoots(rows)).toEqual(new Set(["r1"]));
  });

  it("collapses member (human) comments — only agent actors keep their last", () => {
    const rows = [
      root(comment("m1", "member", "user-1", "2026-09-03T10:00:00Z")),
      root(comment("a1", "agent", "agent-1", "2026-09-01T10:00:00Z")),
    ];
    // The member comment is the globally newest but members are not
    // agents; the agent's only comment still stays expanded.
    expect(computeReentryExpandedRoots(rows)).toEqual(new Set(["a1"]));
  });

  it("ignores activity rows", () => {
    const rows = [
      root({
        type: "activity",
        id: "act-1",
        actor_type: "agent",
        actor_id: "agent-1",
        created_at: "2026-09-05T10:00:00Z",
        action: "changed_status",
      }),
      root(comment("c1", "agent", "agent-1", "2026-09-01T10:00:00Z")),
    ];
    expect(computeReentryExpandedRoots(rows)).toEqual(new Set(["c1"]));
  });

  it("treats an orphan reply promoted to a root row as its own root", () => {
    // buildTimelineRows promotes a reply whose parent is missing to a
    // top-level row — it must still participate in the per-agent scan.
    const entries = [
      comment("orphan", "agent", "agent-1", "2026-09-02T10:00:00Z", {
        parent_id: "missing-parent",
      }),
      root(comment("c1", "agent", "agent-1", "2026-09-01T10:00:00Z")).entry,
    ];
    const rows = buildTimelineRows(entries);
    expect(computeReentryExpandedRoots(rows)).toEqual(new Set(["orphan"]));
  });

  it("breaks created_at ties by later input position (stable ASC contract)", () => {
    const ts = "2026-09-01T10:00:00Z";
    const rows = [
      root(comment("first", "agent", "agent-1", ts)),
      root(comment("second", "agent", "agent-1", ts)),
    ];
    expect(computeReentryExpandedRoots(rows)).toEqual(new Set(["second"]));
  });

  it("expands resolved roots under the same rule (no special-casing)", () => {
    const rows = [
      root(
        comment("c1", "agent", "agent-1", "2026-09-01T10:00:00Z", {
          resolved_at: "2026-09-01T11:00:00Z",
        }),
      ),
      root(comment("c2", "agent", "agent-1", "2026-09-02T10:00:00Z")),
    ];
    expect(computeReentryExpandedRoots(rows)).toEqual(new Set(["c2"]));
  });

  it("does not let a root expand twice for two agents in the same thread", () => {
    const rows = [
      root(comment("r1", "agent", "agent-1", "2026-09-01T10:00:00Z"), [
        comment("r1-reply", "agent", "agent-2", "2026-09-04T10:00:00Z"),
      ]),
      root(comment("r2", "agent", "agent-1", "2026-09-02T10:00:00Z")),
      root(comment("r3", "agent", "agent-2", "2026-09-03T10:00:00Z")),
    ];
    // agent-1's last = r2; agent-2's last = r1's reply → host r1.
    expect(computeReentryExpandedRoots(rows)).toEqual(
      new Set(["r1", "r2"]),
    );
  });
});
