import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
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
