// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { promptQuizKeys } from "@multica/core/self-evolution";
import type { PromptQuizBaseline, PromptQuizSampleRow } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { QuizGradedPanel } from "./quiz-graded-panel";

const api = vi.hoisted(() => ({
  createPromptQuizBatch: vi.fn(),
  getPromptQuizBatch: vi.fn(),
  getCurrentPromptQuizBatch: vi.fn(),
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

function mount(client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
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
    api.getCurrentPromptQuizBatch.mockImplementation(async () => ({
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
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("2 graded");
    expect(within(screen.getByTestId("quiz-graded-batch")).getByTestId(`quiz-sample-${second.task_id}`)).toHaveTextContent(second.task_id);
  });

  it("restores the ordered batch after the panel unmounts and remounts", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getCurrentPromptQuizBatch.mockResolvedValue({
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
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("0 graded");

    first.unmount();
    mount();
    await flush();
    expect(api.getCurrentPromptQuizBatch).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("batch-1");
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("Running");
  });

  it("replaces an in-flight empty current-batch read after ordering a batch", async () => {
    vi.useFakeTimers();
    let resolveInitialRead: (batch: unknown) => void = () => {};
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getCurrentPromptQuizBatch
      .mockImplementationOnce(() => new Promise((resolve) => { resolveInitialRead = resolve; }))
      .mockResolvedValue({
        batch_id: "batch-new",
        ordered: 1,
        settled: false,
        rows: [{ ...row, outcome: "", score: null, task_status: "queued" }],
        counts: {},
        scores: { graded: 0, mean: 0 },
      });
    api.createPromptQuizBatch.mockResolvedValue({ batch_id: "batch-new", ordered: 1 });
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    mount();
    await flush();
    fireEvent.click(screen.getByTestId("quiz-graded-run"));
    await flush();
    await flush();
    expect(api.getCurrentPromptQuizBatch).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("batch-new");

    await act(async () => {
      resolveInitialRead({ batch_id: "", ordered: 0, settled: true, rows: [], counts: {} });
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("batch-new");
  });

  it("retries the current batch after an initial read failure", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getCurrentPromptQuizBatch
      .mockRejectedValueOnce(new Error("temporary batch read failure"))
      .mockResolvedValue({
        batch_id: "batch-1",
        settled: false,
        rows: [{ ...row, outcome: "", score: null, task_status: "running" }],
        counts: {},
        scores: { graded: 0, mean: 0 },
      });
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    mount();
    await flush();
    expect(screen.getByText(enSelfEvolution.quiz.reading.error)).toBeInTheDocument();

    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(api.getCurrentPromptQuizBatch).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("batch-1");
  });

  it("marks terminal tasks without a measured version and stops polling them", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getCurrentPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      ordered: 1,
      settled: true,
      rows: [{ ...row, version: 0, outcome: "", score: null, task_status: "completed", measurement_status: "not_measured" }],
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
    expect(api.getCurrentPromptQuizBatch).toHaveBeenCalledTimes(1);
  });

  it("keeps terminal tasks pending until the server finishes collection", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getCurrentPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      ordered: 1,
      settled: false,
      rows: [{ ...row, version: 0, outcome: "", score: null, task_status: "completed", measurement_status: "pending" }],
      counts: {},
      scores: { graded: 0, mean: 0 },
    });
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    mount();
    await flush();
    expect(screen.getByTestId(`quiz-sample-${row.task_id}`)).toHaveTextContent("Pending");
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(api.getCurrentPromptQuizBatch.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it("resumes polling an older active batch after the newest batch settles", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getCurrentPromptQuizBatch
      .mockResolvedValueOnce({
        batch_id: "batch-new",
        ordered: 1,
        settled: false,
        rows: [{ ...row, outcome: "", score: null, task_status: "running" }],
        counts: {},
        scores: { graded: 0, mean: 0 },
      })
      .mockResolvedValueOnce({
        batch_id: "batch-new",
        ordered: 1,
        settled: true,
        rows: [{ ...row, outcome: "", score: null, task_status: "completed", measurement_status: "not_measured" }],
        counts: {},
        scores: { graded: 0, mean: 0 },
      })
      .mockResolvedValueOnce({
        batch_id: "batch-old",
        ordered: 1,
        settled: false,
        rows: [{ ...row, outcome: "", score: null, task_status: "running" }],
        counts: {},
        scores: { graded: 0, mean: 0 },
      });
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    mount();
    await flush();
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    await flush();
    await flush();
    expect(api.getCurrentPromptQuizBatch.mock.calls.length).toBeGreaterThanOrEqual(3);
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("batch-old");
    expect(screen.getByTestId("quiz-graded-batch")).toHaveTextContent("Running");
  });

  it("recovers a queued batch from the server without serializing it into session storage", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getCurrentPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      ordered: 1,
      settled: false,
      rows: [{ ...row, outcome: "", score: null, task_status: "running" }],
      counts: {},
      scores: { graded: 0, mean: 0 },
    });

    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };
    mount();
    await flush();
    expect(sessionStorage.getItem("multica:quiz:ws-1:agent:agent-1:batch")).toBeNull();
    expect(api.getCurrentPromptQuizBatch).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(api.getCurrentPromptQuizBatch.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it("refreshes samples after an already completed batch mounts", async () => {
    api.getPromptQuizSamples.mockResolvedValue([row]);
    api.getCurrentPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      ordered: 1,
      settled: true,
      rows: [row],
      counts: { answered: 1 },
      scores: { graded: 1, mean: 0.5 },
    });

    mount();
    await screen.findAllByTestId(`quiz-sample-${row.task_id}`);
    await waitFor(() => expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(2));
  });

  it("refreshes cached samples when a restored batch is already settled", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([row]);
    let resolveBatch: (batch: unknown) => void = () => {};
    api.getCurrentPromptQuizBatch.mockImplementationOnce(
      () => new Promise((resolve) => { resolveBatch = resolve; }),
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(promptQuizKeys.samples("ws-1", "agent", "agent-1", 2), []);

    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };
    mount(client);
    await flush();
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId(`quiz-sample-${row.task_id}`)).not.toBeInTheDocument();

    await act(async () => {
      resolveBatch({
        batch_id: "batch-1",
        ordered: 1,
        settled: true,
        rows: [row],
        counts: { answered: 1 },
        scores: { graded: 1, mean: 0.5 },
      });
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(2);
    await flush();
    expect(screen.getAllByTestId(`quiz-sample-${row.task_id}`)).toHaveLength(2);
  });

  it("refetches samples after an in-flight initial request when the batch is already settled", async () => {
    vi.useFakeTimers();
    let resolveInitialSamples: (samples: PromptQuizSampleRow[]) => void = () => {};
    api.getPromptQuizSamples
      .mockImplementationOnce(() => new Promise((resolve) => { resolveInitialSamples = resolve; }))
      .mockResolvedValueOnce([row]);
    api.getCurrentPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      ordered: 1,
      settled: true,
      rows: [row],
      counts: { answered: 1 },
      scores: { graded: 1, mean: 0.5 },
    });

    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };
    mount();
    await flush();
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveInitialSamples([]);
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(2);
    await flush();
    expect(screen.getAllByTestId(`quiz-sample-${row.task_id}`)).toHaveLength(2);
  });

  it("keeps polling placeholder rows until grading evidence is written", async () => {
    vi.useFakeTimers();
    const placeholder = {
      ...row,
      outcome: "answered",
      score: null,
      graded_at: undefined,
      score_detail: undefined,
      task_status: "completed",
    };
    let graded = false;
    api.getPromptQuizSamples.mockImplementation(async () => graded ? [row] : [placeholder]);
    api.getCurrentPromptQuizBatch.mockImplementation(async () => ({
      batch_id: "batch-1",
      rows: [graded ? row : placeholder],
      counts: { answered: 1 },
      scores: { graded: graded ? 1 : 0, mean: graded ? 0.5 : 0 },
    }));
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    mount();
    await flush();
    expect(screen.getAllByTestId(`quiz-sample-${row.task_id}`)).toHaveLength(2);
    expect(screen.getAllByTestId(`quiz-sample-${row.task_id}`)[0]).toHaveTextContent("Not graded");

    graded = true;
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    await flush();
    expect(screen.getAllByTestId(`quiz-sample-${row.task_id}`)).toHaveLength(2);
    expect(screen.getAllByTestId(`quiz-sample-${row.task_id}`)[0]).toHaveTextContent("50%");
    expect(api.getCurrentPromptQuizBatch.mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(api.getPromptQuizSamples.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it("keeps a collected row's task outcome while showing pending grading", async () => {
    const awaitingGrade = {
      ...row,
      outcome: "errored",
      score: null,
      graded_at: undefined,
      score_detail: undefined,
      task_status: "completed",
      measurement_status: "awaiting_grade",
    };
    api.getPromptQuizSamples.mockResolvedValue([awaitingGrade]);
    api.getCurrentPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      ordered: 1,
      settled: false,
      rows: [awaitingGrade],
      counts: { errored: 1 },
      scores: { graded: 0, mean: 0 },
    });

    mount();

    const batch = await screen.findByTestId("quiz-graded-batch");
    expect(within(batch).getByTestId(`quiz-sample-${row.task_id}`)).toHaveTextContent("Awaiting result collection");
    expect(within(batch).getByTestId(`quiz-sample-${row.task_id}`)).toHaveTextContent("errored");
  });

  it("does not poll samples when the server has no current batch", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };

    mount();
    await flush();
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });

    expect(api.getCurrentPromptQuizBatch).toHaveBeenCalledWith("agent-1");
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(1);
  });

  it("does not poll a batch that ordered no rows", async () => {
    vi.useFakeTimers();
    api.getPromptQuizSamples.mockResolvedValue([]);
    api.getCurrentPromptQuizBatch.mockResolvedValue({
      batch_id: "batch-1",
      ordered: 0,
      settled: true,
      rows: [],
      counts: {},
      scores: { graded: 0, mean: 0 },
    });

    const flush = async () => {
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    };
    mount();
    await flush();
    const batchCalls = api.getCurrentPromptQuizBatch.mock.calls.length;
    const sampleCalls = api.getPromptQuizSamples.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });

    expect(api.getCurrentPromptQuizBatch).toHaveBeenCalledTimes(batchCalls);
    expect(api.getPromptQuizSamples).toHaveBeenCalledTimes(sampleCalls);
  });
});
