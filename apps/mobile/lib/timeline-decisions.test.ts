// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { IssueDecision } from "@multica/core/types";
import type { TimelineRow } from "./timeline-thread";
import {
  DECISION_BATCH_BAR_ID,
  interleaveDecisions,
  isDecisionBatchItem,
  isDecisionItem,
} from "./timeline-decisions";

function row(id: string, createdAt: string): TimelineRow {
  return {
    entry: {
      id,
      type: "comment",
      created_at: createdAt,
      actor_type: "member",
      actor_id: "u-1",
    } as TimelineRow["entry"],
    replies: [],
  };
}

function card(id: string, createdAt: string): IssueDecision {
  return {
    id,
    issue_id: "i-1",
    source_comment_id: null,
    question: "q",
    options: [{ label: "A" }, { label: "B" }],
    multi_select: false,
    recommended_indices: [],
    status: "open",
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "a-1",
    created_at: createdAt,
    updated_at: createdAt,
  };
}

function ids(items: ReturnType<typeof interleaveDecisions>): string[] {
  return items.map((item) => item.entry.id);
}

describe("interleaveDecisions", () => {
  it("returns the rows untouched when there are no decisions", () => {
    const rows = [row("c-1", "2026-10-02T03:00:00Z"), row("c-2", "2026-10-02T04:00:00Z")];
    expect(interleaveDecisions(rows, [])).toBe(rows);
  });

  it("places each card at its created_at position among the rows", () => {
    const rows = [
      row("c-1", "2026-10-02T03:00:00Z"),
      row("c-2", "2026-10-02T05:00:00Z"),
    ];
    const merged = interleaveDecisions(rows, [card("d-1", "2026-10-02T04:00:00Z")]);
    expect(ids(merged)).toEqual(["c-1", "d-1", "c-2"]);
  });

  it("breaks timestamp ties in favour of the timeline row", () => {
    const at = "2026-10-02T04:00:00Z";
    const merged = interleaveDecisions([row("c-1", at)], [card("d-1", at)]);
    expect(ids(merged)).toEqual(["c-1", "d-1"]);
  });

  it("keeps multiple cards ordered by created_at and rows stable", () => {
    const rows = [
      row("c-1", "2026-10-02T03:00:00Z"),
      row("c-2", "2026-10-02T03:30:00Z"),
      row("c-3", "2026-10-02T06:00:00Z"),
    ];
    const merged = interleaveDecisions(rows, [
      card("d-2", "2026-10-02T05:00:00Z"),
      card("d-1", "2026-10-02T02:00:00Z"),
    ]);
    // Two open cards ⇒ the batch bar lands at the very END of the timeline
    // (after every comment, activity and card) regardless of the input
    // array order — server numbering order stays created_at ASC inside the
    // bar itself.
    expect(ids(merged)).toEqual([
      "d-1",
      "c-1",
      "c-2",
      "d-2",
      "c-3",
      DECISION_BATCH_BAR_ID,
    ]);
  });

  it("emits decision items with a minimal ordering surface", () => {
    const [item] = interleaveDecisions([], [card("d-1", "2026-10-02T04:00:00Z")]);
    expect(isDecisionItem(item)).toBe(true);
    if (isDecisionItem(item)) {
      expect(item.entry).toEqual({
        id: "d-1",
        created_at: "2026-10-02T04:00:00Z",
      });
      expect(item.decision.id).toBe("d-1");
    }
    expect(isDecisionItem(row("c-1", "2026-10-02T04:00:00Z"))).toBe(false);
  });

  it("appends the batch bar after every timeline row when two or more are open", () => {
    const rows = [row("c-1", "2026-10-02T03:00:00Z"), row("c-2", "2026-10-02T06:00:00Z")];
    const merged = interleaveDecisions(rows, [
      card("d-1", "2026-10-02T04:00:00Z"),
      card("d-2", "2026-10-02T05:00:00Z"),
    ]);
    expect(ids(merged)).toEqual(["c-1", "d-1", "d-2", "c-2", DECISION_BATCH_BAR_ID]);
    const bar = merged.find(isDecisionBatchItem);
    expect(bar?.batchBar.open.map((d) => d.id)).toEqual(["d-1", "d-2"]);
    expect(bar?.entry.id).toBe(DECISION_BATCH_BAR_ID);
    // Comment-only consumers keep skipping the bar (no entry.type, no
    // `decision` field).
    expect("decision" in bar!).toBe(false);
  });

  it("emits no batch bar below two open cards", () => {
    const single = interleaveDecisions(
      [row("c-1", "2026-10-02T03:00:00Z"), row("c-2", "2026-10-02T06:00:00Z")],
      [card("d-1", "2026-10-02T04:00:00Z")],
    );
    expect(single.some(isDecisionBatchItem)).toBe(false);

    const answered = interleaveDecisions([], [
      card("d-1", "2026-10-02T04:00:00Z"),
      card("d-2", "2026-10-02T05:00:00Z"),
    ].map((d, i) => ({ ...d, status: "answered" as const, selected_indices: [i] })));
    expect(answered.some(isDecisionBatchItem)).toBe(false);
  });

  it("counts only open cards: answered ones stay put while open ones get the bar", () => {
    const answered = { ...card("d-old", "2026-10-02T02:00:00Z"), status: "answered" as const, selected_indices: [0] };
    const merged = interleaveDecisions([], [
      answered,
      card("d-1", "2026-10-02T04:00:00Z"),
      card("d-2", "2026-10-02T05:00:00Z"),
    ]);
    expect(ids(merged)).toEqual(["d-old", "d-1", "d-2", DECISION_BATCH_BAR_ID]);
  });
});
