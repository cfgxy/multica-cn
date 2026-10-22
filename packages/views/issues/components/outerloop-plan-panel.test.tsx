// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mutations = vi.hoisted(() => ({
  save: vi.fn().mockResolvedValue(undefined),
  fire: vi.fn(),
  stop: vi.fn(),
}));
const draft = vi.hoisted(() => ({ value: undefined as { goal: string; maxRounds: number } | undefined }));

vi.mock("@multica/core/config", () => ({ useFeatureEnabled: () => true }));
vi.mock("@multica/core/paths", () => ({ useCurrentWorkspace: () => ({ id: "ws-1" }) }));
vi.mock("@multica/core/issues/outerloop-queries", () => ({
  outerLoopPlanOptions: () => ({ queryKey: ["outerloop"], queryFn: vi.fn() }),
  useUpdateOuterLoopPlan: () => ({ mutateAsync: mutations.save, isPending: false, error: null }),
  useFireOuterLoopPlan: () => ({ mutate: mutations.fire, isPending: false, error: null, data: undefined }),
  useStopOuterLoopPlan: () => ({ mutate: mutations.stop, isPending: false, error: null }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    isLoading: false,
    data: { has_plan: true, plan: { state: "waiting_human", goal: "完成验收", max_rounds: 8, round: 3, wait_reason: "等待批准" } },
  }),
}));
vi.mock("@multica/core/issues/stores/outerloop-plan-draft-store", () => ({
  useOuterLoopPlanDraftStore: (selector: (state: any) => unknown) => selector({
    drafts: draft.value ? { "issue-1": draft.value } : {},
    setDraft: (_id: string, value: { goal: string; maxRounds: number }) => { draft.value = value; },
    clearDraft: () => { draft.value = undefined; },
  }),
}));

import { OuterLoopPlanPanel } from "./outerloop-plan-panel";

describe("OuterLoopPlanPanel", () => {
  beforeEach(() => { mutations.save.mockClear(); draft.value = { goal: "完成验收", maxRounds: 8 }; });

  it("shows plan progress and only writes after preview confirmation", async () => {
    render(<OuterLoopPlanPanel issueId="issue-1" />);
    expect(screen.getByText("第 3/8 轮 · waiting_human")).toBeTruthy();
    expect(screen.getByText("等待原因：等待批准")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "预览并保存" }));
    expect(mutations.save).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "确认保存" }));
    expect(mutations.save).toHaveBeenCalledWith({ enabled: true, goal: "完成验收", max_rounds: 8 });
  });
});
