// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { IssueDecision } from "@multica/core/types";
import { getBatchDecisionCandidates } from "./decision-batch";

function decision(
  id: string,
  createdAt: string,
  decisionKind?: IssueDecision["decision_kind"],
): IssueDecision {
  return {
    id,
    issue_id: "issue-1",
    source_comment_id: null,
    question: id,
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
    created_by_id: "agent-1",
    created_at: createdAt,
    updated_at: createdAt,
    decision_kind: decisionKind,
  };
}

describe("getBatchDecisionCandidates", () => {
  it("excludes open authorization cards and sorts remaining open decisions", () => {
    const candidates = getBatchDecisionCandidates([
      decision("authorization", "2026-01-01T00:00:00Z", "authorization"),
      decision("later", "2026-01-03T00:00:00Z"),
      decision("earlier", "2026-01-02T00:00:00Z"),
    ]);

    expect(candidates.map(({ id }) => id)).toEqual(["earlier", "later"]);
  });
});
