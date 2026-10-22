// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { setCurrentWorkspace } from "../../platform/workspace-storage";
import { useOuterLoopPlanDraftStore } from "./outerloop-plan-draft-store";

describe("outer loop plan drafts", () => {
  beforeEach(() => {
    localStorage.clear();
    setCurrentWorkspace(null, null);
    useOuterLoopPlanDraftStore.setState({ drafts: {} });
  });

  afterEach(() => setCurrentWorkspace(null, null));

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

  it("rehydrates the workspace-scoped draft after a reload", async () => {
    localStorage.setItem(
      "multica_outerloop_plan_drafts:acme",
      JSON.stringify({ state: { drafts: { "issue-a": { goal: "完成 M4", maxRounds: 8 } } }, version: 0 }),
    );

    setCurrentWorkspace("acme", "ws_a");
    await Promise.resolve();
    await Promise.resolve();

    expect(useOuterLoopPlanDraftStore.getState().drafts).toEqual({
      "issue-a": { goal: "完成 M4", maxRounds: 8 },
    });
  });
});
