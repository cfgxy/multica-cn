// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { PromptQuizBaseline, PromptQuizSampleRow } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { QuizGradedPanel } from "./quiz-graded-panel";
import { writeQuizBatch } from "./quiz-session";

const api = vi.hoisted(() => ({
  createPromptQuizBatch: vi.fn(),
  getPromptQuizBatch: vi.fn(),
  getPromptQuizSamples: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api,
  clientErrorMessage: (error: unknown) => error instanceof Error ? error.message : undefined,
}));

const baseline = {
  scope: "agent",
  scope_id: "agent-1",
  current_version: 2,
  baseline_version: 1,
  required_sample: 12,
  required_baseline: 30,
  current: { n: 12, mean: 0.55, median: 0.5, iqr: 0.2, std_dev: 0.15, min: 0.2, max: 0.9 },
  measured: true,
  outcomes: { answered: 1, errored: 0 },
  incomparable: 0,
  baseline_incomparable: 0,
} as PromptQuizBaseline;

const row: PromptQuizSampleRow = {
  task_id: "0199abcd-0000-7000-8000-000000000001",
  scope: "agent",
  scope_id: "agent-1",
  version: 2,
  item_id: "item-1",
  item_revision: 1,
  item_title: "Follow instructions",
  outcome: "answered",
  score: 0.5,
  measured_at: "2026-09-30T10:00:00Z",
  graded_at: "2026-09-30T10:05:00Z",
  score_detail: [{ id: "rule-1", kind: "includes", passed: true, evidence: "matched" }],
};

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <QuizGradedPanel wsId="ws-1" agentId="agent-1" baseline={baseline} />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { ...view, client };
}

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
  sessionStorage.clear();
});

describe("QuizGradedPanel", () => {
  it("identifies each sample's run and both timestamps beside its evidence", async () => {
    api.getPromptQuizSamples.mockResolvedValue([row]);
    mount();
    const sample = await screen.findByTestId(`quiz-sample-${row.task_id}`);
    expect(api.getPromptQuizSamples).toHaveBeenCalledWith({ scope: "agent", scopeId: "agent-1", version: 2 });
    expect(sample).toHaveTextContent(row.task_id);
    expect(sample.querySelector(`time[datetime="${row.measured_at}"]`)).not.toBeNull();
    expect(sample.querySelector(`time[datetime="${row.graded_at}"]`)).not.toBeNull();
    expect(sample).toHaveTextContent("rule-1");
  });

  // Fake timers must be active before mounting: the batch query schedules its
  // refetchInterval timer on success, and a timer created under real timers is
  // invisible to vi.advanceTimersByTimeAsync.
  it("refreshes an ordered batch and its samples after asynchronous grading", async () => {
    vi.useFakeTimers();
    let latestSamples: PromptQuizSampleRow[] = [];
    const pendingRow = {
      ...row,
      task_id: "0199abcd-0000-7000-8000-000000000002",
      outcome: "" as const,
      score: null,
      task_status: "running",
    };
    api.getPromptQuizSamples.mockImplementation(async () => latestSamples);
    api.getPromptQuizBatch.mockImplementation(async () => ({
      batch_id: "batch-1", rows: [row, pendingRow],
      counts: { answered: 1 },
      scores: { graded: latestSamples.length, mean: 0.5 },
    }));
    api.createPromptQuizBatch.mockResolvedValue({ batch_id: "batch-1", ordered: 1 });
    // Positive advance: react-query's notifyManager schedules observer
    // notifications via setTimeout(0), which a 0ms tick does not run.
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };
    mount();
    await flush();
    expect(screen.getByText(enSelfEvolution.quiz.graded.samplesEmpty)).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("quiz-graded-run"));
    await flush();
    await flush();
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("0 graded");

    latestSamples = [row];
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("1 graded");
    expect(within(screen.getByTestId("quiz-graded-batch")).getByTestId(`quiz-sample-${row.task_id}`)).toHaveTextContent(row.task_id);
    await flush();
    await flush();
    expect(screen.getAllByTestId(`quiz-sample-${row.task_id}`)).toHaveLength(2);
    expect(api.getPromptQuizSamples.mock.calls.length).toBeGreaterThanOrEqual(2);

    const secondRow = { ...row, task_id: pendingRow.task_id, item_title: "Second question" };
    latestSamples = [row, secondRow];
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(screen.getAllByTestId(`quiz-sample-${secondRow.task_id}`)).toHaveLength(2);
  });

  it("restores the ordered batch after the panel unmounts and remounts", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      rows: [{ ...row, outcome: "", score: null, task_status: "running" }],
      counts: {},
      scores: { graded: 0, mean: 0 },
    });
    api.createPromptQuizBatch.mockResolvedValue({ batch_id: "batch-1", ordered: 1 });
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    const first = mount();
    await flush();
    fireEvent.click(screen.getByTestId("quiz-graded-run"));
    await flush();
    await flush();
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("Running");

    first.unmount();
    mount();
    await flush();
    expect(api.getPromptQuizBatch).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("batch-1");
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("Running");
  });

  it("marks terminal tasks without a measured version and stops polling them", async () => {
    vi.useFakeTimers();
    writeQuizBatch("ws-1", "agent-1", "batch-1");
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      rows: [{ ...row, version: 0, outcome: "", score: null, task_status: "completed" }],
      counts: {},
      scores: { graded: 0, mean: 0 },
    });
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    mount();
    await flush();
    expect(screen.getByTestId(`quiz-sample-${row.task_id}`)).toHaveTextContent("Not measured");
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(api.getPromptQuizBatch).toHaveBeenCalledTimes(1);
  });

  it("continues polling a restored batch after the former polling window", async () => {
    vi.useFakeTimers();
    sessionStorage.setItem(
      "multica:quiz:ws-1:agent:agent-1:batch",
      JSON.stringify({ batchId: "batch-1", orderedAt: Date.now() - 71 * 60 * 1000 }),
    );
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      rows: [{ ...row, outcome: "", score: null, task_status: "running" }],
      counts: {},
      scores: { graded: 0, mean: 0 },
    });

    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };
    mount();
    await flush();
    expect(api.getPromptQuizBatch).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(api.getPromptQuizBatch.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it("does not refetch samples twice when an already completed batch mounts", async () => {
    writeQuizBatch("ws-1", "agent-1", "batch-1");
    api.getPromptQuizSamples.mockResolvedValue([row]);
    api.getPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      rows: [row],
      counts: { answered: 1 },
      scores: { graded: 1, mean: 0.5 },
    });

    mount();
    await screen.findAllByTestId(`quiz-sample-${row.task_id}`);
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(1);
  });
});
