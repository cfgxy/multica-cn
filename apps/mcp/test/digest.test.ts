import { describe, expect, it } from "vitest";

import { buildDigest, type DigestInput } from "../src/digest.js";
import type { IssueInfo } from "../src/types.js";

function issue(over: Partial<IssueInfo>): IssueInfo {
  return {
    id: "id",
    identifier: "WS-1",
    number: 1,
    title: "t",
    status: "todo",
    ...over,
  };
}

function digestInput(over: Partial<DigestInput>): DigestInput {
  return {
    workspace: "ws",
    generatedAt: new Date("2026-09-05T10:00:00Z"),
    counts: [],
    recentlyActive: [],
    dueQueue: [],
    ...over,
  };
}

describe("buildDigest", () => {
  it("sums open totals excluding done/cancelled", () => {
    const digest = buildDigest(
      digestInput({
        counts: [
          { status: "backlog", total: 3 },
          { status: "todo", total: 5 },
          { status: "in_progress", total: 2 },
          { status: "in_review", total: 1 },
          { status: "blocked", total: 1 },
          { status: "done", total: 40 },
          { status: "cancelled", total: 7 },
        ],
      }),
    );
    expect(digest.open_total).toBe(12);
    expect(digest.counts_by_status["done"]).toBe(40);
  });

  it("classifies overdue vs due soon from the ascending due-date queue", () => {
    const digest = buildDigest(
      digestInput({
        dueQueue: [
          issue({ identifier: "WS-2", due_date: "2026-09-01", status: "in_progress" }),
          issue({ identifier: "WS-3", due_date: "2026-09-04", status: "todo" }),
          issue({ identifier: "WS-4", due_date: "2026-09-05", status: "todo" }),
          issue({ identifier: "WS-5", due_date: "2026-09-20", status: "in_review" }),
        ],
      }),
    );
    expect(digest.overdue.map((ref) => ref.identifier)).toEqual(["WS-2", "WS-3"]);
    expect(digest.due_soon.map((ref) => ref.identifier)).toEqual(["WS-4", "WS-5"]);
  });

  it("never marks a terminal-status issue overdue", () => {
    const digest = buildDigest(
      digestInput({
        dueQueue: [
          issue({ identifier: "WS-6", due_date: "2026-09-01", status: "done" }),
        ],
      }),
    );
    expect(digest.overdue).toHaveLength(0);
    expect(digest.due_soon).toHaveLength(0);
  });

  it("notes a possibly truncated overdue list when the window is all overdue", () => {
    const digest = buildDigest(
      digestInput({
        dueQueue: [
          issue({ identifier: "WS-7", due_date: "2026-08-01", status: "todo" }),
          issue({ identifier: "WS-8", due_date: "2026-08-02", status: "todo" }),
        ],
      }),
    );
    expect(digest.notes).toHaveLength(1);
    expect(digest.notes[0]).toMatch(/truncated/);
  });

  it("maps recently active issues to refs", () => {
    const digest = buildDigest(
      digestInput({
        recentlyActive: [
          issue({ identifier: "WS-9", title: "voice idea", status: "in_progress" }),
        ],
      }),
    );
    expect(digest.recently_active[0]?.identifier).toBe("WS-9");
    expect(digest.recently_active[0]?.title).toBe("voice idea");
  });

  it("stamps generated_at in ISO format", () => {
    const digest = buildDigest(digestInput({}));
    expect(digest.generated_at).toBe("2026-09-05T10:00:00.000Z");
  });
});
