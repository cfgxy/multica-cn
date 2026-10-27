// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type {
  PromptQuizBaseline,
  PromptQuizItem,
  PromptQuizItemDetail,
} from "@multica/core/types";
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
  detail: null as PromptQuizItemDetail | null,
  role: "owner" as string | null,
  created: [] as unknown[],
  updated: [] as unknown[],
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
    promptQuizItemOptions: (wsId: string, itemId: string) => ({
      queryKey: ["prompt-quiz", wsId, "item", itemId],
      queryFn: () => Promise.resolve(state.detail),
      enabled: itemId !== "",
    }),
    useCreatePromptQuizItem: () => ({
      isPending: false,
      mutate: (body: unknown) => state.created.push(body),
    }),
    useUpdatePromptQuizItem: () => ({
      isPending: false,
      mutate: (body: unknown) => state.updated.push(body),
    }),
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
    outcomes: { answered: 12, errored: 0 },
    incomparable: 0,
    baseline_incomparable: 0,
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

function detail(over: Partial<PromptQuizItemDetail> = {}): PromptQuizItemDetail {
  return { ...item(), rubric: "Names all three constraints.", ...over };
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
  state.detail = detail();
  state.role = "owner";
  state.created = [];
  state.updated = [];
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

  it("accounts for answered and errored runs without claiming either is a score", async () => {
    // Migration 933 narrowed the outcome vocabulary: "answered" says a run
    // produced an answer, not that the answer was right. The card prints both
    // counts and, in the same sentence, that neither is a grade — a bare
    // "8 passed" would be read as a pass rate the data cannot support.
    state.baseline = baseline({ outcomes: { answered: 8, errored: 3 } });
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-outcomes")).toBeInTheDocument();
    });
    const line = screen.getByTestId("quiz-outcomes");
    expect(line).toHaveTextContent("8");
    expect(line).toHaveTextContent("3");
    expect(line.textContent ?? "").toContain("Neither count is a score");
    // The word the old vocabulary used, and the reason blocker 2 was raised:
    // nothing on this card may suggest an answer was judged correct.
    expect(screen.queryByText(/pass rate|passed/i)).not.toBeInTheDocument();
  });

  it("says how many readings were excluded, and why, on both sides", async () => {
    // Without this line a bank edit and a collection outage look identical: the
    // group is simply smaller than the number of runs that were made.
    state.baseline = baseline({ incomparable: 4, baseline_incomparable: 7 });
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-incomparable")).toBeInTheDocument();
    });
    expect(screen.getByTestId("quiz-incomparable")).toHaveTextContent("4");
    expect(screen.getByTestId("quiz-incomparable-baseline")).toHaveTextContent("7");
  });

  it("hides the exclusion lines when every reading is comparable", async () => {
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-reading-card")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("quiz-incomparable")).not.toBeInTheDocument();
    expect(screen.queryByTestId("quiz-incomparable-baseline")).not.toBeInTheDocument();
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

  it("marks a question that no longer separates two versions", async () => {
    // A4: the mark is shown where the decision to reword or retire is made.
    state.items = [item({ discrimination: "flat" })];
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-item-discrimination-summarize-a-thread")).toBeInTheDocument();
    });
    expect(screen.getByTestId("quiz-item-discrimination-summarize-a-thread")).toHaveTextContent(
      enSelfEvolution.quiz.bank.discrimination.flat,
    );
  });

  it("shows a mark it cannot read as unjudged rather than as fine", async () => {
    state.items = [item({ discrimination: "excellent" })];
    renderTab();
    await waitFor(() => {
      expect(screen.getByTestId("quiz-item-discrimination-summarize-a-thread")).toBeInTheDocument();
    });
    expect(screen.getByTestId("quiz-item-discrimination-summarize-a-thread")).toHaveTextContent(
      enSelfEvolution.quiz.bank.discrimination.pending,
    );
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
        rubric: "",
      },
    ]);
  });

  it("edits a question through the owner-only read so the answer key survives", async () => {
    // The list has no rubric, and an update replaces the rubric whole. Seeding
    // the form from a list row would silently save an empty answer key over the
    // stored one, so the editor loads the question through the single read.
    renderTab();
    fireEvent.click(await screen.findByText(enSelfEvolution.quiz.bank.edit));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => {
      expect(dialog.querySelectorAll("textarea").length).toBe(2);
    });
    const areas = dialog.querySelectorAll("textarea");
    await waitFor(() => {
      expect((areas[1] as HTMLTextAreaElement).value).toBe("Names all three constraints.");
    });
    fireEvent.change(areas[0]!, { target: { value: "Summarize it in two bullets." } });
    fireEvent.click(screen.getByText(enSelfEvolution.quiz.bank.dialog.save));
    expect(state.updated).toEqual([
      {
        itemId: "item-1",
        patch: {
          title: "Summarize a thread",
          body: "Summarize it in two bullets.",
          rubric: "Names all three constraints.",
          runtime_profile: "member",
          active: true,
        },
      },
    ]);
  });

  it("cannot save an edit whose question has not loaded yet", async () => {
    // The reverse verification for the read above: with the detail unavailable
    // the draft is empty, and saving it would overwrite the stored body and
    // answer key with blanks.
    state.detail = null;
    renderTab();
    fireEvent.click(await screen.findByText(enSelfEvolution.quiz.bank.edit));
    await screen.findByRole("dialog");
    fireEvent.click(screen.getByText(enSelfEvolution.quiz.bank.dialog.save));
    expect(state.updated).toEqual([]);
  });
});
