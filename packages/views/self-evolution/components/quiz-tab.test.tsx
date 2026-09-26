// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { PromptQuizBaseline, PromptQuizItem } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { QuizTab } from "./quiz-tab";

/**
 * The quiz tab's wiring (RUYI-185).
 *
 * The state fold is covered canonically in
 * `packages/core/self-evolution/quiz.test.ts`. What only a mount can show is
 * asserted here: that an unmeasured version renders no verdict badge at all,
 * that the "does not block publishing" line is on the card in every state, and
 * that a member sees no bank write controls.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  baseline: null as PromptQuizBaseline | null,
  items: [] as PromptQuizItem[],
  role: "owner" as string | null,
  created: [] as unknown[],
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: (wsId: string) => ({
    queryKey: ["agents", wsId],
    queryFn: () => Promise.resolve([{ id: "agent-1", name: "Gu Xiaoyu" }]),
  }),
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ userId: "u-1", role: state.role, member: null, isLoading: false }),
}));

vi.mock("@multica/core/api", () => ({
  api: {},
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
}));

vi.mock("@multica/core/self-evolution", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/self-evolution")>(
      "@multica/core/self-evolution",
    );
  return {
    ...actual,
    promptQuizBaselineOptions: (wsId: string, scope: string, scopeId: string) => ({
      queryKey: ["prompt-quiz", wsId, "baseline", scope, scopeId],
      queryFn: () => Promise.resolve(state.baseline),
      enabled: scopeId !== "",
    }),
    promptQuizItemsOptions: (wsId: string) => ({
      queryKey: ["prompt-quiz", wsId, "items"],
      queryFn: () => Promise.resolve(state.items),
    }),
    useCreatePromptQuizItem: () => ({
      isPending: false,
      mutate: (body: unknown) => state.created.push(body),
    }),
    useUpdatePromptQuizItem: () => ({ isPending: false, mutate: () => {} }),
    useDeletePromptQuizItem: () => ({ isPending: false, mutate: () => {} }),
  };
});

function summary(over: Record<string, number> = {}) {
  return { n: 12, mean: 100, median: 95, iqr: 20, std_dev: 30, min: 60, max: 200, ...over };
}

function baseline(over: Partial<PromptQuizBaseline> = {}): PromptQuizBaseline {
  return {
    scope: "agent",
    scope_id: "agent-1",
    current_version: 3,
    baseline_version: 2,
    required_sample: 12,
    required_baseline: 30,
    measured: true,
    current: summary(),
    outcomes: { passed: 10, failed: 2 },
    comparison: {
      baseline: summary({ n: 30, median: 140, iqr: 25 }),
      current: summary(),
      z: -2.4,
      threshold: 1.96,
      verdict: "improved",
    },
    ...over,
  };
}

function item(over: Partial<PromptQuizItem> = {}): PromptQuizItem {
  return {
    id: "item-1",
    slug: "summarize-a-thread",
    title: "Summarize a thread",
    body: "Summarize the discussion below in three bullets.",
    revision: 2,
    runtime_profile: "member",
    active: true,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-02T00:00:00Z",
    ...over,
  };
}

function renderTab(agentId = "agent-1") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <QuizTab wsId="ws-1" initialAgentId={agentId} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  state.baseline = baseline();
  state.items = [item()];
  state.role = "owner";
  state.created = [];
});

describe("QuizTab reading", () => {
  it("prints the verdict with both groups and the decision inputs", async () => {
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-verdict")).toBeInTheDocument();
    });
    expect(screen.getByTestId("quiz-verdict")).toHaveTextContent(
      enSelfEvolution.quiz.reading.verdict.improved,
    );
    // Both medians and the statistic/threshold pair, so the call can be
    // recomputed from the card rather than trusted.
    expect(screen.getByText(/median 140, IQR 25, n=30/)).toBeInTheDocument();
    expect(screen.getByText(/median 95, IQR 20, n=12/)).toBeInTheDocument();
    expect(screen.getByText("-2.4")).toBeInTheDocument();
    expect(screen.getByText("1.96")).toBeInTheDocument();
  });

  it("renders no verdict badge while the sample is still accumulating", async () => {
    state.baseline = baseline({ measured: false, current: summary({ n: 4 }), comparison: undefined });
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-state-accumulating")).toBeInTheDocument();
    });
    // The whole point: an unmeasured version must not acquire a label. Not
    // "steady", not a 0 — no badge at all.
    expect(screen.queryByTestId("quiz-verdict")).not.toBeInTheDocument();
    expect(screen.getByTestId("quiz-state-accumulating")).toHaveTextContent("4 of 12");
  });

  it("says a first version has nothing to compare against", async () => {
    state.baseline = baseline({ baseline_version: 0, comparison: undefined });
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-state-no-baseline")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("quiz-verdict")).not.toBeInTheDocument();
  });

  it("says nothing was measured when the scope has no version", async () => {
    state.baseline = baseline({ current_version: 0 });
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-state-no-version")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("quiz-verdict")).not.toBeInTheDocument();
  });

  it("keeps the not-a-gate line on the card in every state", async () => {
    // Owner decision Q10: a quiz records how quality changed and never blocks a
    // version from being published. The card has to say so where the verdict is
    // read, not in a doc nobody opens.
    for (const b of [
      baseline(),
      baseline({ current_version: 0 }),
      baseline({ measured: false, current: summary({ n: 0 }), comparison: undefined }),
    ]) {
      state.baseline = b;
      const { unmount } = renderTab();
      await waitFor(() => {
        expect(screen.getByText(enSelfEvolution.quiz.reading.notAGate)).toBeInTheDocument();
      });
      unmount();
    }
  });

  it("reports errored runs separately from the reading", async () => {
    state.baseline = baseline({ outcomes: { passed: 8, failed: 1, errored: 3 } });
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-errored")).toBeInTheDocument();
    });
    expect(screen.getByTestId("quiz-errored")).toHaveTextContent("3");
  });

  it("hides the errored line when nothing errored", async () => {
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-reading-card")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("quiz-errored")).not.toBeInTheDocument();
  });

  it("asks for a subject before reading anything", () => {
    renderTab("");
    expect(screen.getByText(enSelfEvolution.quality.noSubject.title)).toBeInTheDocument();
    expect(screen.queryByTestId("quiz-reading-card")).not.toBeInTheDocument();
  });
});

describe("QuizTab bank", () => {
  it("lists a question with its revision and active state", async () => {
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-item-summarize-a-thread")).toBeInTheDocument();
    });
    const row = screen.getByTestId("quiz-item-summarize-a-thread");
    expect(row).toHaveTextContent("rev 2");
    expect(row).toHaveTextContent(enSelfEvolution.quiz.bank.active);
  });

  it("offers no write controls to a non-owner", async () => {
    // Bank writes are owner-only on the server. Admin is deliberately excluded:
    // a body edit changes what every later version is measured against.
    state.role = "admin";
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-item-summarize-a-thread")).toBeInTheDocument();
    });
    expect(screen.queryByText(enSelfEvolution.quiz.bank.add)).not.toBeInTheDocument();
    expect(screen.queryByText(enSelfEvolution.quiz.bank.edit)).not.toBeInTheDocument();
    expect(screen.queryByText(enSelfEvolution.quiz.bank.delete)).not.toBeInTheDocument();
  });

  it("tells an owner the bank is empty rather than showing a bare list", async () => {
    state.items = [];
    renderTab();
    await waitFor(() => {
      expect(screen.getByText(enSelfEvolution.quiz.bank.empty.title)).toBeInTheDocument();
    });
  });

  it("submits a new question with the slug, title and body it collected", async () => {
    renderTab();
    fireEvent.click(await screen.findByText(enSelfEvolution.quiz.bank.add));
    const dialog = await screen.findByRole("dialog");
    const inputs = dialog.querySelectorAll("input");
    const textarea = dialog.querySelector("textarea");
    if (inputs.length < 2 || textarea === null) throw new Error("dialog fields missing");
    fireEvent.change(inputs[0]!, { target: { value: "explain-a-tradeoff" } });
    fireEvent.change(inputs[1]!, { target: { value: "Explain a tradeoff" } });
    fireEvent.change(textarea, { target: { value: "Compare two approaches." } });
    fireEvent.click(screen.getByText(enSelfEvolution.quiz.bank.dialog.save));
    expect(state.created).toEqual([
      {
        slug: "explain-a-tradeoff",
        title: "Explain a tradeoff",
        body: "Compare two approaches.",
      },
    ]);
  });
});
