// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { IssueDecision, IssueDecisionStatus } from "../types";
import {
  decisionBrowserNeighbor,
  filterDecisionCards,
  initialDecisionBrowserCardId,
  resolveDecisionBrowserPosition,
} from "./decision-card-browser";

// RUYI-863: canonical position-state matrix for the decision-card browser.
// Web/desktop/mobile all import these helpers — the components keep the
// happy path only and point here for the state rules.

function card(
  id: string,
  status: IssueDecisionStatus = "open",
  created: string = "2026-01-01T00:00:00Z",
): IssueDecision {
  return {
    id,
    issue_id: "issue-1",
    source_comment_id: null,
    question: `q-${id}`,
    options: [],
    multi_select: false,
    selected_indices: [],
    recommended_indices: [],
    status,
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "agent-1",
    created_at: created,
    updated_at: created,
  };
}

const list = [
  card("c1", "answered"),
  card("c2", "cancelled"),
  card("c3", "open"),
  card("c4", "open"),
];

// RUYI-630 authorization card: open, but its lifecycle lives on the
// decision request — never a member of the "open" bucket.
const AUTH_OPEN = card("auth-1", "open");
AUTH_OPEN.decision_kind = "authorization";
const withAuth = [...list, AUTH_OPEN];

describe("filterDecisionCards", () => {
  it("keeps created_at order under every filter", () => {
    expect(filterDecisionCards(list, "all").map((d) => d.id)).toEqual([
      "c1",
      "c2",
      "c3",
      "c4",
    ]);
    expect(filterDecisionCards(list, "open").map((d) => d.id)).toEqual([
      "c3",
      "c4",
    ]);
    expect(filterDecisionCards(list, "cancelled").map((d) => d.id)).toEqual(["c2"]);
    expect(filterDecisionCards(list, "answered")).toHaveLength(1);
  });

  it("keeps open authorization cards out of the pending bucket", () => {
    expect(filterDecisionCards(withAuth, "open").map((d) => d.id)).toEqual([
      "c3",
      "c4",
    ]);
    // "all" still shows the card for history.
    expect(filterDecisionCards(withAuth, "all")).toHaveLength(5);
  });
});

describe("initialDecisionBrowserCardId", () => {
  it("pins the target card under any status", () => {
    // Single-card entry: an answered target must NOT bounce elsewhere.
    expect(initialDecisionBrowserCardId(list, "all", "c1")).toBe("c1");
    expect(initialDecisionBrowserCardId(list, "all", "c2")).toBe("c2");
  });

  it("ignores a pin outside the entry filter and lands on the first match", () => {
    expect(initialDecisionBrowserCardId(list, "open", "c1")).toBe("c3");
  });

  it("lands on the first card of the filter without a pin", () => {
    expect(initialDecisionBrowserCardId(list, "open")).toBe("c3");
    expect(initialDecisionBrowserCardId(list, "cancelled")).toBe("c2");
    expect(initialDecisionBrowserCardId(list, "open")).not.toBeNull();
    expect(initialDecisionBrowserCardId([], "all")).toBeNull();
  });

  it("falls back to the first matching card when the pinned one is gone", () => {
    expect(initialDecisionBrowserCardId(list, "all", "nope")).toBe("c1");
  });
});

describe("resolveDecisionBrowserPosition", () => {
  it("stays on the desired card while it matches the filter", () => {
    expect(resolveDecisionBrowserPosition(list, "all", "c2")).toBe("c2");
    expect(resolveDecisionBrowserPosition(list, "open", "c4")).toBe("c4");
  });

  it("advances to the next matching card when the desired left the bucket", () => {
    // Answered c3 under "open" → next pending is c4.
    const afterAnswer = list.map((d) =>
      d.id === "c3" ? card(d.id, "answered") : d,
    );
    expect(resolveDecisionBrowserPosition(afterAnswer, "open", "c3")).toBe("c4");
  });

  it("wraps to the first match when the desired card was the last one", () => {
    const afterAnswer = list.map((d) =>
      d.id === "c4" ? card(d.id, "answered") : d,
    );
    expect(resolveDecisionBrowserPosition(afterAnswer, "open", "c4")).toBe("c3");
  });

  it("returns null when the filter matches nothing (done/empty state)", () => {
    const allAnswered = list.map((d) => card(d.id, "answered"));
    expect(resolveDecisionBrowserPosition(allAnswered, "open", "c3")).toBeNull();
  });

  it("lands on the first card when the desired is unknown", () => {
    expect(resolveDecisionBrowserPosition(list, "open", null)).toBe("c3");
    expect(resolveDecisionBrowserPosition(list, "open", "gone")).toBe("c3");
  });

  it("never lands an open bucket on an authorization card", () => {
    // auth-1 (created last) is open but excluded; the bucket ends at c4.
    expect(initialDecisionBrowserCardId(withAuth, "open")).toBe("c3");
    // Desired card outside the bucket and last in order → wrap to c3.
    expect(resolveDecisionBrowserPosition(withAuth, "open", "auth-1")).toBe("c3");
    expect(decisionBrowserNeighbor(withAuth, "open", "c4", "next")).toBeNull();
  });
});

describe("decisionBrowserNeighbor", () => {
  it("walks the filtered sequence only", () => {
    expect(decisionBrowserNeighbor(list, "open", "c3", "next")).toBe("c4");
    expect(decisionBrowserNeighbor(list, "open", "c4", "next")).toBeNull();
    expect(decisionBrowserNeighbor(list, "open", "c4", "previous")).toBe("c3");
    expect(decisionBrowserNeighbor(list, "open", "c3", "previous")).toBeNull();
    expect(decisionBrowserNeighbor(list, "all", "c2", "next")).toBe("c3");
  });

  it("returns null for a card outside the filter or unknown", () => {
    expect(decisionBrowserNeighbor(list, "open", "c2", "next")).toBeNull();
    expect(decisionBrowserNeighbor(list, "open", null, "next")).toBeNull();
  });
});
