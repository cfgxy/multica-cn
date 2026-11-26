// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mutations = vi.hoisted(() => ({
  save: vi.fn().mockResolvedValue(undefined),
  fire: vi.fn(),
  stop: vi.fn(),
  firePending: false,
  fireData: undefined as { dispatched: boolean; reason?: string } | undefined,
  fireError: null as Error | null,
}));
const draft = vi.hoisted(() => ({ value: undefined as { goal: string; maxRounds: number } | undefined }));
const feature = vi.hoisted(() => ({ enabled: true }));

vi.mock("@multica/core/config", () => ({ useFeatureEnabled: () => feature.enabled }));
vi.mock("@multica/core/paths", () => ({ useCurrentWorkspace: () => ({ id: "ws-1" }) }));
vi.mock("@multica/core/issues/outerloop-queries", () => ({
  outerLoopPlanOptions: () => ({ queryKey: ["outerloop"], queryFn: vi.fn() }),
  useUpdateOuterLoopPlan: () => ({ mutateAsync: mutations.save, isPending: false, error: null }),
  useFireOuterLoopPlan: () => ({ mutate: mutations.fire, isPending: mutations.firePending, error: mutations.fireError, data: mutations.fireData }),
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
  beforeEach(() => {
    mutations.save.mockClear();
    mutations.firePending = false;
    mutations.fireData = undefined;
    mutations.fireError = null;
    feature.enabled = true;
    draft.value = { goal: "完成验收", maxRounds: 8 };
  });
  afterEach(cleanup);

  it("shows plan progress and only writes after preview confirmation", async () => {
    render(<OuterLoopPlanPanel issueId="issue-1" />);
    expect(screen.getByText("第 3/8 轮 · waiting_human")).toBeTruthy();
    expect(screen.getByText("等待原因：等待批准")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "预览并保存" }));
    expect(mutations.save).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "确认保存" }));
    expect(mutations.save).toHaveBeenCalledWith({ enabled: true, goal: "完成验收", max_rounds: 8 });
  });

  it("hides the panel when the feature flag is unavailable", () => {
    feature.enabled = false;
    const { container } = render(<OuterLoopPlanPanel issueId="issue-1" />);
    expect(container.innerHTML).toBe("");
  });

  it("shows pending-task and mutation-error feedback", () => {
    mutations.fireData = { dispatched: false, reason: "pending_task" };
    mutations.fireError = new Error("续跑失败");
    render(<OuterLoopPlanPanel issueId="issue-1" />);
    expect(screen.getByText("已有待处理续跑任务")).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("续跑失败");
  });
});
