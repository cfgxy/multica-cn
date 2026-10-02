import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { IssueDecision } from "../types";
import { patchDecisionInCache } from "./decisions";

function card(id: string, status: IssueDecision["status"] = "open"): IssueDecision {
  return {
    id,
    issue_id: "i-1",
    source_comment_id: null,
    question: "q",
    options: [{ label: "A" }, { label: "B" }],
    multi_select: false,
    recommended_indices: [],
    status,
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "a-1",
    created_at: "2026-10-02T03:00:00Z",
    updated_at: "2026-10-02T03:00:00Z",
  };
}

describe("patchDecisionInCache", () => {
  it("replaces the matching card by id and keeps list order", () => {
    const qc = new QueryClient();
    qc.setQueryData(["issues", "decisions", "i-1"], [card("d-1"), card("d-2")]);

    patchDecisionInCache(qc, "i-1", card("d-2", "answered"));

    const list = qc.getQueryData<IssueDecision[]>(["issues", "decisions", "i-1"]);
    expect(list?.map((d) => `${d.id}:${d.status}`)).toEqual(["d-1:open", "d-2:answered"]);
  });

  it("leaves the cache untouched for an unknown issue or card id", () => {
    const qc = new QueryClient();
    const prev = [card("d-1")];
    qc.setQueryData(["issues", "decisions", "i-1"], prev);

    patchDecisionInCache(qc, "i-other", card("d-1", "answered"));
    patchDecisionInCache(qc, "i-1", card("d-x", "answered"));

    expect(qc.getQueryData(["issues", "decisions", "i-1"])).toBe(prev);
  });
});
