import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { IssueDecision } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { DecisionCard } from "./decision-card";

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: (type: string) => (type === "agent" ? "顾小鱼" : "成员甲") }),
}));

const answerSpy = vi.fn();
const cancelSpy = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    answerIssueDecision: (...a: unknown[]) => answerSpy(...a),
    cancelIssueDecision: (...a: unknown[]) => cancelSpy(...a),
  },
}));

function card(overrides: Partial<IssueDecision> = {}): IssueDecision {
  return {
    id: "d-1",
    issue_id: "i-1",
    source_comment_id: null,
    question: "走哪条方案？",
    options: [{ label: "方案 A" }, { label: "方案 B" }, { label: "方案 C" }],
    multi_select: false,
    recommended_indices: [1],
    status: "open",
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "a-1",
    created_at: "2026-10-02T03:00:00Z",
    updated_at: "2026-10-02T03:00:00Z",
    ...overrides,
  };
}

function renderCard(decision: IssueDecision) {
  const qc = new QueryClient();
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <DecisionCard decision={decision} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  answerSpy.mockReset().mockResolvedValue(card({ status: "answered", selected_indices: [0] }));
  cancelSpy.mockReset().mockResolvedValue(card({ status: "cancelled" }));
});

describe("DecisionCard", () => {
  it("renders question, options and the recommended badge in open state", () => {
    renderCard(card());
    expect(screen.getByTestId("decision-card")).toHaveAttribute("data-status", "open");
    expect(screen.getByText("走哪条方案？")).toBeInTheDocument();
    expect(screen.getByText("方案 B")).toBeInTheDocument();
    expect(screen.getByText("Recommended")).toBeInTheDocument();
  });

  // RUYI-588: the single card renders its own index letter per row — the
  // same letter language as the batch bar (RUYI-471) — so cards whose labels
  // no longer embed an "A：" prefix still show A/B/C/D.
  it("renders an index letter per option row, batch-bar form", () => {
    renderCard(card());
    const row0 = screen.getByTestId("decision-option-0");
    expect(within(row0).getByText("A")).toBeInTheDocument();
    expect(within(screen.getByTestId("decision-option-1")).getByText("B")).toBeInTheDocument();
    expect(within(screen.getByTestId("decision-option-2")).getByText("C")).toBeInTheDocument();
    expect(within(row0).getByText("方案 A")).toBeInTheDocument();
    // Letter form parity with the batch bar: medium weight, brand-tinted
    // when its row is picked, muted otherwise.
    fireEvent.click(row0);
    const letter = within(screen.getByTestId("decision-option-0")).getByText("A");
    expect(letter).toHaveClass("font-medium");
    expect(letter).toHaveClass("text-brand");
    expect(within(screen.getByTestId("decision-option-1")).getByText("B")).toHaveClass("text-muted-foreground");
  });

  // RUYI-575's guard, single-card side: labels written under the
  // decision-numbering convention embed the letter itself ("A：…") — the
  // card's own index letter must not double it.
  it("strips the embedded letter prefix so each letter renders once", () => {
    renderCard(card({
      question: "Stage 2 实施边界：本期 capability 覆盖面？",
      options: [
        { label: "A：仅 /file/ 云盘文件族（drive_file_links）" },
        { label: "B：A 之上同期加 wiki_doc_links" },
      ],
    }));
    const first = screen.getByTestId("decision-option-0");
    expect(within(first).getByText("A")).toBeInTheDocument();
    expect(within(first).getByText("仅 /file/ 云盘文件族（drive_file_links）")).toBeInTheDocument();
    expect(within(first).queryByText("A：仅 /file/ 云盘文件族（drive_file_links）")).toBeNull();
    const second = screen.getByTestId("decision-option-1");
    expect(within(second).getByText("B")).toBeInTheDocument();
    expect(within(second).getByText("A 之上同期加 wiki_doc_links")).toBeInTheDocument();
    expect(within(second).queryByText("B：A 之上同期加 wiki_doc_links")).toBeNull();
  });

  it("renders labels without a matching prefix form untouched", () => {
    renderCard(card({ options: [{ label: "A-type 优先" }, { label: "B超 声呐" }, { label: "方案 C" }] }));
    expect(within(screen.getByTestId("decision-option-0")).getByText("A-type 优先")).toBeInTheDocument();
    expect(within(screen.getByTestId("decision-option-1")).getByText("B超 声呐")).toBeInTheDocument();
    expect(within(screen.getByTestId("decision-option-2")).getByText("方案 C")).toBeInTheDocument();
  });

  it("single-select replaces the previous pick before submit", () => {
    renderCard(card());
    fireEvent.click(screen.getByTestId("decision-option-0"));
    fireEvent.click(screen.getByTestId("decision-option-2"));
    expect(screen.getByTestId("decision-option-0")).toHaveAttribute("aria-checked", "false");
    expect(screen.getByTestId("decision-option-2")).toHaveAttribute("aria-checked", "true");
  });

  it("submits the picked option", async () => {
    renderCard(card());
    fireEvent.click(screen.getByTestId("decision-option-0"));
    fireEvent.click(screen.getByRole("button", { name: "Submit answer" }));
    await waitFor(() => expect(answerSpy).toHaveBeenCalledWith("i-1", "d-1", [0]));
  });

  it("disables submit until a pick exists", () => {
    renderCard(card());
    expect(screen.getByRole("button", { name: "Submit answer" })).toBeDisabled();
  });

  it("multi-select allows several picks and uses the multi submit label", async () => {
    renderCard(card({ multi_select: true }));
    fireEvent.click(screen.getByTestId("decision-option-0"));
    fireEvent.click(screen.getByTestId("decision-option-2"));
    expect(screen.getByTestId("decision-option-0")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByTestId("decision-option-2")).toHaveAttribute("aria-checked", "true");
    fireEvent.click(screen.getByRole("button", { name: "Submit selection" }));
    await waitFor(() => expect(answerSpy).toHaveBeenCalledWith("i-1", "d-1", [0, 2]));
  });

  it("answered cards show picks and hide interactions", () => {
    renderCard(card({ status: "answered", selected_indices: [1], answered_by_type: "member", answered_by_id: "m-1", answered_at: "2026-10-02T04:00:00Z" }));
    expect(screen.getByTestId("decision-card")).toHaveAttribute("data-status", "answered");
    expect(screen.getByTestId("decision-option-1")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByTestId("decision-option-1")).toBeDisabled();
    expect(within(screen.getByTestId("decision-option-1")).getByText("B")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Submit answer" })).not.toBeInTheDocument();
    expect(screen.getByText("成员甲")).toBeInTheDocument();
  });

  it("cancelled cards render without interactions", () => {
    renderCard(card({ status: "cancelled" }));
    expect(screen.getByTestId("decision-card")).toHaveAttribute("data-status", "cancelled");
    expect(screen.getByTestId("decision-option-0")).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Submit answer" })).not.toBeInTheDocument();
  });
});
