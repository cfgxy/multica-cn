// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { PromptQuizBaseline, PromptQuizSampleRow } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { QuizGradedPanel } from "./quiz-graded-panel";

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
    let latest: PromptQuizSampleRow[] = [];
    api.getPromptQuizSamples.mockImplementation(async () => latest);
    api.getPromptQuizBatch.mockImplementation(async () => ({
      batch_id: "batch-1", rows: latest,
      counts: latest.length > 0 ? { answered: 1 } : {},
      scores: { graded: latest.length, mean: 0.5 },
    }));
    api.createPromptQuizBatch.mockResolvedValue({ batch_id: "batch-1", ordered: 2 });
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

    latest = [row];
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("1 graded");
    expect(within(screen.getByTestId("quiz-graded-batch")).getByTestId(`quiz-sample-${row.task_id}`)).toHaveTextContent(row.task_id);

    const second = { ...row, task_id: "0199abcd-0000-7000-8000-000000000002" };
    latest = [row, second];
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("2 graded");
    expect(within(screen.getByTestId("quiz-graded-batch")).getByTestId(`quiz-sample-${second.task_id}`)).toHaveTextContent(second.task_id);
  });

  it("restores the ordered batch after the panel unmounts and remounts", async () => {
    vi.useFakeTimers();
    let latest: PromptQuizSampleRow[] = [];
    api.getPromptQuizSamples.mockImplementation(async () => latest);
    api.getPromptQuizBatch.mockImplementation(async () => ({
      batch_id: "batch-1",
      rows: latest,
      counts: latest.length > 0 ? { answered: latest.length } : {},
      scores: { graded: latest.length, mean: latest.length > 0 ? 0.5 : 0 },
    }));
    api.createPromptQuizBatch.mockResolvedValue({ batch_id: "batch-1", ordered: 1 });
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    const first = mount();
    await flush();
    fireEvent.click(screen.getByTestId("quiz-graded-run"));
    await flush();
    await flush();
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("0 graded");

    first.unmount();
    mount();
    await flush();
    expect(api.getPromptQuizBatch).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("batch-1");
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("0 graded");

    latest = [row];
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("1 graded");
    expect(screen.getAllByTestId(`quiz-sample-${row.task_id}`)).toHaveLength(2);
  });

  it("stops batch and sample polling when the window expires without results", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      rows: [],
      counts: {},
      scores: { graded: 0, mean: 0 },
    });
    api.createPromptQuizBatch.mockResolvedValue({ batch_id: "batch-1", ordered: 1 });
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    mount();
    await flush();
    fireEvent.click(screen.getByTestId("quiz-graded-run"));
    await flush();
    await flush();

    await act(async () => { await vi.advanceTimersByTimeAsync(10 * 60 * 1000); });
    const batchCallsAtDeadline = api.getPromptQuizBatch.mock.calls.length;
    const sampleCallsAtDeadline = api.getPromptQuizSamples.mock.calls.length;

    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(api.getPromptQuizBatch).toHaveBeenCalledTimes(batchCallsAtDeadline);
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(sampleCallsAtDeadline);
  });

  it("does not restart sample polling after remounting an expired ordered batch", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      rows: [],
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
    await act(async () => { await vi.advanceTimersByTimeAsync(10 * 60 * 1000); });

    first.unmount();
    const sampleCallsBeforeRemount = api.getPromptQuizSamples.mock.calls.length;
    mount();
    await flush();
    const sampleCallsAfterRemount = api.getPromptQuizSamples.mock.calls.length;
    expect(sampleCallsAfterRemount).toBe(sampleCallsBeforeRemount + 1);

    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(sampleCallsAfterRemount);
  });
});
