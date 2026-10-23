import { describe, expect, it } from "vitest";
import type { WorkspaceDecisionInbox } from "@multica/core/types";
import {
  groupDecisionInboxSections,
  isDecidedDecisionRow,
  resolveDecisionActorType,
  toDecisionInboxRow,
} from "./decision-inbox-display";

function makeItem(
  over: Partial<WorkspaceDecisionInbox["items"][number]> & { id: string },
): WorkspaceDecisionInbox["items"][number] {
  return {
    workspace_id: "ws-1",
    issue_id: "issue-1",
    issue_number: 494,
    issue_identifier: "RUYI-494",
    issue_title: "Decision Inbox",
    question: "Choose an option",
    options: [{ label: "A" }, { label: "B" }],
    multi_select: false,
    recommended_indices: [],
    status: "open",
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "member",
    created_by_id: "u1",
    created_at: "2026-10-07T00:00:00Z",
    updated_at: "2026-10-07T00:00:00Z",
    ...over,
  } as WorkspaceDecisionInbox["items"][number];
}

function makePage(
  items: WorkspaceDecisionInbox["items"],
  counts: WorkspaceDecisionInbox["counts"],
): WorkspaceDecisionInbox {
  return {
    items,
    counts,
    next_cursor: null,
    next_cursor_id: null,
  };
}

// RUYI-494 acceptance at the shaping layer: the list's data unit is the CARD
// (same issue with 3 cards = 3 rows, AC2), sections run open → answered →
// cancelled with 待决策 first (AC7), counts stay the server's filter-window
// totals, and the recommendation flag feeds the 「有推荐」 badge (AC10).
describe("toDecisionInboxRow", () => {
  it("builds the fixed two-line row surface", () => {
    const row = toDecisionInboxRow(
      makeItem({
        id: "d1",
        recommended_indices: [1],
        created_at: "2026-10-07T09:00:00Z",
      }),
    );
    expect(row.identifier).toBe("RUYI-494");
    expect(row.issueTitle).toBe("Decision Inbox");
    expect(row.question).toBe("Choose an option");
    expect(row.recommended).toBe(true);
    expect(row.createdAt).toBe("2026-10-07T09:00:00Z");
  });

  it("falls back to #<issue_number> when the identifier join is empty", () => {
    const row = toDecisionInboxRow(makeItem({ id: "d2", issue_identifier: "" }));
    expect(row.identifier).toBe("#494");
  });

  it("never folds two cards of one issue into one row (AC2)", () => {
    const a = toDecisionInboxRow(makeItem({ id: "d1", question: "first" }));
    const b = toDecisionInboxRow(makeItem({ id: "d2", question: "second" }));
    expect(a.id).not.toBe(b.id);
    expect(a.question).not.toBe(b.question);
  });
});

// RUYI-864: the grouping walks PAGES (keyset) instead of one full list. The
// section counts come off the last page so they stay the filter-window
// totals while the page walk is in flight.
describe("groupDecisionInboxSections", () => {
  it("groups by status, 待决策 first, preserving server order inside", () => {
    const sections = groupDecisionInboxSections([
      makePage(
        [
          makeItem({ id: "o2", created_at: "2026-10-07T10:00:00Z" }),
          makeItem({ id: "o1", created_at: "2026-10-07T09:00:00Z" }),
          makeItem({ id: "a1", status: "answered", selected_indices: [0] }),
          makeItem({ id: "x1", status: "cancelled" }),
        ],
        { open: 2, answered: 1, cancelled: 1 },
      ),
    ]);
    expect(sections.map((s) => s.key)).toEqual(["open", "answered", "cancelled"]);
    expect(sections[0]!.rows.map((r) => r.id)).toEqual(["o2", "o1"]);
  });

  it("merges rows across pages in fetch order", () => {
    const sections = groupDecisionInboxSections([
      makePage([makeItem({ id: "o1" })], { open: 2, answered: 0, cancelled: 0 }),
      makePage([makeItem({ id: "o2" })], { open: 2, answered: 0, cancelled: 0 }),
    ]);
    expect(sections[0]!.rows.map((r) => r.id)).toEqual(["o1", "o2"]);
  });

  it("keeps the LAST page's window counts as section totals, not row counts", () => {
    const sections = groupDecisionInboxSections([
      makePage([makeItem({ id: "o1" })], { open: 1, answered: 0, cancelled: 0 }),
      makePage([makeItem({ id: "o2" })], { open: 7, answered: 3, cancelled: 1 }),
    ]);
    expect(sections).toHaveLength(1);
    expect(sections[0]!.count).toBe(7);
  });

  it("drops empty sections entirely (web parity)", () => {
    const sections = groupDecisionInboxSections([
      makePage(
        [makeItem({ id: "o1" })],
        { open: 1, answered: 0, cancelled: 0 },
      ),
    ]);
    expect(sections.map((s) => s.key)).toEqual(["open"]);
  });

  it("renders the empty inbox as no sections", () => {
    expect(
      groupDecisionInboxSections([
        makePage([], { open: 0, answered: 0, cancelled: 0 }),
      ]),
    ).toEqual([]);
  });

  it("renders no sections when no page has been fetched yet", () => {
    expect(groupDecisionInboxSections([])).toEqual([]);
  });
});

// ── RUYI-530 ────────────────────────────────────────────────────────────────
// 三项优化在数据整形层的契约：行携带发起者身份（头像数据面）、已决策灰化
// 判定（answered/cancelled 灰、open 不灰——负向断言）。
describe("RUYI-530 toDecisionInboxRow creator identity", () => {
  it("carries the card creator's actor identity for the avatar", () => {
    const row = toDecisionInboxRow(
      makeItem({ id: "d1", created_by_type: "agent", created_by_id: "agent-1" }),
    );
    expect(row.createdByType).toBe("agent");
    expect(row.createdById).toBe("agent-1");
  });
});

describe("RUYI-530 resolveDecisionActorType", () => {
  it("maps known creator types through", () => {
    expect(resolveDecisionActorType("agent")).toBe("agent");
    expect(resolveDecisionActorType("member")).toBe("member");
  });

  it("falls back to system for null/empty/unknown types (avatar never blank)", () => {
    expect(resolveDecisionActorType(null)).toBe("system");
    expect(resolveDecisionActorType(undefined)).toBe("system");
    expect(resolveDecisionActorType("")).toBe("system");
    expect(resolveDecisionActorType("bot")).toBe("system");
  });
});

describe("RUYI-530 isDecidedDecisionRow (graying predicate)", () => {
  it("grays answered and cancelled rows", () => {
    expect(isDecidedDecisionRow("answered")).toBe(true);
    expect(isDecidedDecisionRow("cancelled")).toBe(true);
  });

  it("does NOT gray open rows (negative assertion)", () => {
    expect(isDecidedDecisionRow("open")).toBe(false);
  });
});
