import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { IssueDecision } from "@multica/core/types";
import { issueKeys } from "@multica/core/issues/queries";
import { renderWithI18n } from "../../test/i18n";
import { DecisionBatchBar } from "./decision-batch-bar";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), message: vi.fn() },
}));

const batchSpy = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    answerIssueDecisionsBatch: (...a: unknown[]) => batchSpy(...a),
  },
}));

function card(id: string, overrides: Partial<IssueDecision> = {}): IssueDecision {
  return {
    id,
    issue_id: "i-1",
    source_comment_id: null,
    question: `问题 ${id}？`,
    options: [{ label: "方案 A" }, { label: "方案 B" }],
    multi_select: false,
    recommended_indices: [],
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

function renderBar(open: IssueDecision[]) {
  const qc = new QueryClient();
  qc.setQueryData(issueKeys.decisions("i-1"), open);
  const utils = renderWithI18n(
    <QueryClientProvider client={qc}>
      <DecisionBatchBar issueId="i-1" open={open} />
    </QueryClientProvider>,
  );
  return { ...utils, qc };
}

beforeEach(() => {
  batchSpy.mockReset();
});

describe("DecisionBatchBar", () => {
  it("renders one numbered row per open card with lettered options", () => {
    renderBar([card("d-1"), card("d-2")]);
    expect(screen.getByTestId("decision-batch-bar")).toBeInTheDocument();
    expect(screen.getByTestId("decision-batch-count")).toHaveTextContent("2");
    expect(screen.getByText("问题 d-1？")).toBeInTheDocument();
    expect(screen.getByText("问题 d-2？")).toBeInTheDocument();
    expect(screen.getByTestId(`decision-batch-option-d-1-0`)).toHaveTextContent("A");
    expect(screen.getByTestId(`decision-batch-option-d-2-1`)).toHaveTextContent("B");
  });

  // RUYI-575: labels written under the decision-numbering convention embed the
  // letter itself ("A：…") while the bar renders its own index letter — the
  // prefix must be stripped or the letter shows twice (RUYI-572 real card).
  it("strips the embedded letter prefix so each letter renders once", () => {
    renderBar([
      card("d-572", {
        question: "Stage 2 实施边界：本期 capability 覆盖面？（详见 ADR 005 §8 决策 1）",
        options: [
          { label: "A：仅 /file/ 云盘文件族（drive_file_links），wiki/docx 另立后续单" },
          { label: "B：A 之上同期加 wiki_doc_links（wiki get_node + docx raw_content）" },
        ],
      }),
    ]);

    const first = screen.getByTestId("decision-batch-option-d-572-0");
    expect(within(first).getByText("仅 /file/ 云盘文件族（drive_file_links），wiki/docx 另立后续单")).toBeInTheDocument();
    expect(within(first).queryByText("A：仅 /file/ 云盘文件族（drive_file_links），wiki/docx 另立后续单")).toBeNull();
    const second = screen.getByTestId("decision-batch-option-d-572-1");
    expect(within(second).getByText("A 之上同期加 wiki_doc_links（wiki get_node + docx raw_content）")).toBeInTheDocument();
    expect(within(second).queryByText("B：A 之上同期加 wiki_doc_links（wiki get_node + docx raw_content）")).toBeNull();
  });

  it("renders labels without a matching prefix form untouched", () => {
    renderBar([card("d-neg", { options: [{ label: "A-type 优先" }, { label: "B超 声呐" }] })]);

    expect(within(screen.getByTestId("decision-batch-option-d-neg-0")).getByText("A-type 优先")).toBeInTheDocument();
    expect(within(screen.getByTestId("decision-batch-option-d-neg-1")).getByText("B超 声呐")).toBeInTheDocument();
  });

  it("submit stays disabled until at least one card has a pick", () => {
    renderBar([card("d-1"), card("d-2")]);
    expect(screen.getByTestId("decision-batch-submit")).toBeDisabled();
    fireEvent.click(screen.getByTestId("decision-batch-option-d-1-0"));
    expect(screen.getByTestId("decision-batch-submit")).toBeEnabled();
  });

  it("single-select replaces the previous pick on the same card", () => {
    renderBar([card("d-1"), card("d-2")]);
    fireEvent.click(screen.getByTestId("decision-batch-option-d-1-0"));
    fireEvent.click(screen.getByTestId("decision-batch-option-d-1-1"));
    expect(screen.getByTestId("decision-batch-option-d-1-0")).toHaveAttribute("aria-checked", "false");
    expect(screen.getByTestId("decision-batch-option-d-1-1")).toHaveAttribute("aria-checked", "true");
  });

  it("submits all picked cards in one batch call and patches the cache", async () => {
    const open = [card("d-1"), card("d-2")];
    batchSpy.mockResolvedValue({
      results: [
        { decision_id: "d-1", status: "answered", decision: { ...open[0]!, status: "answered", selected_indices: [0] } },
        { decision_id: "d-2", status: "answered", decision: { ...open[1]!, status: "answered", selected_indices: [1] } },
      ],
      echo_comment_id: "c-echo",
    });
    const { qc } = renderBar(open);

    fireEvent.click(screen.getByTestId("decision-batch-option-d-1-0"));
    fireEvent.click(screen.getByTestId("decision-batch-option-d-2-1"));
    fireEvent.click(screen.getByTestId("decision-batch-submit"));

    await waitFor(() =>
      expect(batchSpy).toHaveBeenCalledWith("i-1", [
        { decision_id: "d-1", selected_indices: [0] },
        { decision_id: "d-2", selected_indices: [1] },
      ]),
    );
    await waitFor(() => {
      const cached = qc.getQueryData<IssueDecision[]>(issueKeys.decisions("i-1"));
      expect(cached?.[0]?.status).toBe("answered");
      expect(cached?.[1]?.status).toBe("answered");
    });
  });

  it("unpicked cards are left out of the batch (partial answers allowed)", async () => {
    batchSpy.mockResolvedValue({
      results: [
        { decision_id: "d-1", status: "answered", decision: card("d-1", { status: "answered", selected_indices: [0] }) },
      ],
    });
    renderBar([card("d-1"), card("d-2")]);

    fireEvent.click(screen.getByTestId("decision-batch-option-d-1-0"));
    fireEvent.click(screen.getByTestId("decision-batch-submit"));

    await waitFor(() =>
      expect(batchSpy).toHaveBeenCalledWith("i-1", [{ decision_id: "d-1", selected_indices: [0] }]),
    );
  });

  it("multi-select cards accumulate picks sorted by index", async () => {
    batchSpy.mockResolvedValue({
      results: [{ decision_id: "d-1", status: "answered", decision: card("d-1", { status: "answered" }) }],
    });
    renderBar([card("d-1", { multi_select: true }), card("d-2")]);

    fireEvent.click(screen.getByTestId("decision-batch-option-d-1-1"));
    fireEvent.click(screen.getByTestId("decision-batch-option-d-1-0"));
    fireEvent.click(screen.getByTestId("decision-batch-submit"));

    await waitFor(() =>
      expect(batchSpy).toHaveBeenCalledWith("i-1", [
        expect.objectContaining({ decision_id: "d-1", selected_indices: [0, 1] }),
      ]),
    );
  });

  it("per-card failures surface the error toast and the batch never throws", async () => {
    const { toast } = await import("sonner");
    batchSpy.mockResolvedValue({
      results: [
        { decision_id: "d-1", status: "answered", decision: card("d-1", { status: "answered", selected_indices: [0] }) },
        { decision_id: "d-2", status: "conflict", error: "already answered" },
      ],
    });
    const { qc } = renderBar([card("d-1"), card("d-2")]);

    fireEvent.click(screen.getByTestId("decision-batch-option-d-1-0"));
    fireEvent.click(screen.getByTestId("decision-batch-submit"));

    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    // The answered half is still patched; the conflict half waits for the
    // invalidated refetch.
    await waitFor(() => {
      const cached = qc.getQueryData<IssueDecision[]>(issueKeys.decisions("i-1"));
      expect(cached?.[0]?.status).toBe("answered");
    });
    // Picks are cleared after submit; with no pick left the bar sits quiet
    // until the survivor card is answered again.
    expect(screen.getByTestId("decision-batch-submit")).toBeDisabled();
  });
});
