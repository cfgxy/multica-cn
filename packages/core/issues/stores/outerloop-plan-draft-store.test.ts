import { beforeEach, describe, expect, it } from "vitest";
import { useOuterLoopPlanDraftStore } from "./outerloop-plan-draft-store";

describe("outer loop plan drafts", () => {
  beforeEach(() => useOuterLoopPlanDraftStore.setState({ drafts: {} }));

  it("keeps drafts isolated by issue and removes them after confirmation", () => {
    const store = useOuterLoopPlanDraftStore.getState();
    store.setDraft("issue-a", { goal: "完成 M4", maxRounds: 8 });
    store.setDraft("issue-b", { goal: "修复测试", maxRounds: 4 });
    expect(useOuterLoopPlanDraftStore.getState().drafts).toEqual({
      "issue-a": { goal: "完成 M4", maxRounds: 8 },
      "issue-b": { goal: "修复测试", maxRounds: 4 },
    });
    store.clearDraft("issue-a");
    expect(useOuterLoopPlanDraftStore.getState().drafts).toEqual({
      "issue-b": { goal: "修复测试", maxRounds: 4 },
    });
  });
});
