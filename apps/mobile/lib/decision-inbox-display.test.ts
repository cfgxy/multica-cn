import { describe, expect, it } from "vitest";
import type { WorkspaceDecisionInbox } from "@multica/core/types";
import {
  filterDecisionRows,
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

// RUYI-494 acceptance at the shaping layer: the list's data unit is the CARD
// (same issue with 3 cards = 3 rows, AC2), sections run open → answered →
// cancelled with 待决策 first (AC7), counts stay the server's workspace
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

describe("groupDecisionInboxSections", () => {
  it("groups by status, 待决策 first, preserving server order inside", () => {
    const sections = groupDecisionInboxSections({
      items: [
        makeItem({ id: "o2", created_at: "2026-10-07T10:00:00Z" }),
        makeItem({ id: "o1", created_at: "2026-10-07T09:00:00Z" }),
        makeItem({ id: "a1", status: "answered", selected_indices: [0] }),
        makeItem({ id: "x1", status: "cancelled" }),
      ],
      counts: { open: 2, answered: 1, cancelled: 1 },
    });
    expect(sections.map((s) => s.key)).toEqual(["open", "answered", "cancelled"]);
    expect(sections[0]!.rows.map((r) => r.id)).toEqual(["o2", "o1"]);
  });

  it("keeps server counts as section totals, not row counts", () => {
    const sections = groupDecisionInboxSections({
      items: [makeItem({ id: "o1" })],
      counts: { open: 7, answered: 3, cancelled: 1 },
    });
    expect(sections).toHaveLength(1);
    expect(sections[0]!.count).toBe(7);
  });

  it("drops empty sections entirely (web parity)", () => {
    const sections = groupDecisionInboxSections({
      items: [makeItem({ id: "o1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
    });
    expect(sections.map((s) => s.key)).toEqual(["open"]);
  });

  it("renders the empty inbox as no sections", () => {
    expect(
      groupDecisionInboxSections({
        items: [],
        counts: { open: 0, answered: 0, cancelled: 0 },
      }),
    ).toEqual([]);
  });
});

// ── RUYI-530 ────────────────────────────────────────────────────────────────
// 三项优化在数据整形层的契约：行携带发起者身份（头像数据面）、已决策灰化
// 判定（answered/cancelled 灰、open 不灰——负向断言）、TAB/筛选逻辑
// （状态 TAB 映射 + 有推荐 / Agent 发起两个筛选维度，叠加为 AND）。
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

describe("RUYI-530 filterDecisionRows (TAB + filter mechanics)", () => {
  const rows = [
    {
      ...toDecisionInboxRow(makeItem({ id: "a1", status: "answered", recommended_indices: [0] })),
      createdByType: "agent",
      createdById: "agent-1",
    },
    {
      ...toDecisionInboxRow(makeItem({ id: "a2", status: "answered" })),
      createdByType: "member",
      createdById: "u1",
    },
    {
      ...toDecisionInboxRow(makeItem({ id: "o1", status: "open", recommended_indices: [0] })),
      createdByType: "agent",
      createdById: "agent-1",
    },
    {
      ...toDecisionInboxRow(makeItem({ id: "x1", status: "cancelled" })),
      createdByType: "agent",
      createdById: "agent-2",
    },
  ];

  it("tab=all keeps every status; a status tab narrows to it", () => {
    const all = filterDecisionRows(rows, { tab: "all", recommendedOnly: false, agentCreatedOnly: false });
    expect(all.map((r) => r.id)).toEqual(["a1", "a2", "o1", "x1"]);
    expect(filterDecisionRows(rows, { tab: "open", recommendedOnly: false, agentCreatedOnly: false }).map((r) => r.id)).toEqual(["o1"]);
    expect(filterDecisionRows(rows, { tab: "answered", recommendedOnly: false, agentCreatedOnly: false }).map((r) => r.id)).toEqual(["a1", "a2"]);
    expect(filterDecisionRows(rows, { tab: "cancelled", recommendedOnly: false, agentCreatedOnly: false }).map((r) => r.id)).toEqual(["x1"]);
  });

  it("recommendedOnly keeps only cards with recommendations, across tabs", () => {
    expect(filterDecisionRows(rows, { tab: "all", recommendedOnly: true, agentCreatedOnly: false }).map((r) => r.id)).toEqual(["a1", "o1"]);
    expect(filterDecisionRows(rows, { tab: "answered", recommendedOnly: true, agentCreatedOnly: false }).map((r) => r.id)).toEqual(["a1"]);
  });

  it("agentCreatedOnly keeps only agent-created cards", () => {
    expect(filterDecisionRows(rows, { tab: "all", recommendedOnly: false, agentCreatedOnly: true }).map((r) => r.id)).toEqual(["a1", "o1", "x1"]);
  });

  it("composes tab and both filters as AND", () => {
    expect(
      filterDecisionRows(rows, { tab: "answered", recommendedOnly: true, agentCreatedOnly: true }).map((r) => r.id),
    ).toEqual(["a1"]);
    // 成员发起 + 有推荐 + answered → 无命中，空集而非报错。
    expect(
      filterDecisionRows(rows, { tab: "open", recommendedOnly: true, agentCreatedOnly: false }).map((r) => r.id),
    ).toEqual(["o1"]);
  });
});
