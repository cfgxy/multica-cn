// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { PromptQualityDashboard } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { QualityTab } from "./quality-tab";

/**
 * The quality tab's wiring and its degradation branches (RUYI-184).
 *
 * The state matrix itself is covered canonically in
 * `packages/core/self-evolution/measure.test.ts`; what is asserted here is what
 * only a mount can show — that a "no data" or "sample too small" dimension
 * never reaches the screen as a number, and that an optional export being off
 * stays a footer line.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const dashboardRef = vi.hoisted(() => ({ current: null as PromptQualityDashboard | null }));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: (wsId: string) => ({
    queryKey: ["agents", wsId],
    queryFn: () => Promise.resolve([{ id: "agent-1", name: "Gu Xiaoyu" }]),
  }),
}));

vi.mock("@multica/core/self-evolution", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/self-evolution")>(
      "@multica/core/self-evolution",
    );
  return {
    ...actual,
    promptQualityDashboardOptions: (
      wsId: string,
      scope: string,
      scopeId: string,
      days: number,
    ) => ({
      queryKey: ["prompt-quality", wsId, scope, scopeId, days],
      queryFn: () => Promise.resolve(dashboardRef.current),
      enabled: scopeId !== "",
    }),
  };
});

function measure(over: Record<string, unknown> = {}) {
  return {
    state: "ok",
    value: 0.5,
    numerator: 5,
    denominator: 10,
    sample: 10,
    threshold: 10,
    ...over,
  };
}

function dashboard(over: Partial<PromptQualityDashboard> = {}): PromptQualityDashboard {
  return {
    scope: "agent",
    scope_id: "agent-1",
    since: "2026-09-01",
    window: {
      version: 0,
      days: 30,
      runs: 40,
      measures: {
        injected_tokens: measure({ value: 12000, numerator: null, denominator: null }),
        run_tokens_median: measure({ value: 80000, numerator: null, denominator: null }),
        // T5: instrumented only from migration 924 onward — NULL is not 0%.
        discipline: measure({
          state: "no_data",
          value: null,
          numerator: null,
          denominator: null,
          reason: "not_instrumented",
        }),
        // T7: below the declared floor — no rate, not a zero.
        tool_failure_rate: measure({
          state: "insufficient_sample",
          value: null,
          numerator: null,
          denominator: null,
          sample: 3,
          threshold: 10,
          reason: "below_sample_floor",
        }),
        retry_rate: measure({ value: 0.2, numerator: 8, denominator: 40 }),
        failure_attribution: measure({ value: 0.25, numerator: 1, denominator: 4, excluded: 2 }),
        first_pass_rate: measure({ value: 0.75, numerator: 3, denominator: 4 }),
      },
      failure_reasons: { prompt_ambiguity: 1 },
      excluded_failed_runs: 2,
    },
    versions: [],
    perplexity: [],
    data_sources: { degraded: false, items: [] },
    ...over,
  } as PromptQualityDashboard;
}

function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <QualityTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  dashboardRef.current = dashboard();
});

describe("QualityTab", () => {
  it("asks for a subject before loading anything", () => {
    renderTab();
    expect(screen.getByText(enSelfEvolution.quality.noSubject.title)).toBeInTheDocument();
  });
});

describe("QualityTab with a subject", () => {
  // The picker is a Base UI select; driving it through the DOM would test the
  // primitive, so the subject is supplied by rendering with one preselected
  // through the same code path the picker uses.
  function renderWithAgent() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <QueryClientProvider client={client}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <QualityTab wsId="ws-1" initialAgentId="agent-1" />
        </I18nProvider>
      </QueryClientProvider>,
    );
  }

  it("renders the seven cards with the measured values", async () => {
    renderWithAgent();
    await waitFor(() => {
      expect(screen.getByTestId("quality-card-first_pass_rate")).toBeInTheDocument();
    });
    for (const key of [
      "injected_tokens",
      "run_tokens_median",
      "discipline",
      "tool_failure_rate",
      "retry_rate",
      "failure_attribution",
      "first_pass_rate",
    ]) {
      expect(screen.getByTestId(`quality-card-${key}`)).toBeInTheDocument();
    }
    expect(screen.getByTestId("quality-card-retry_rate")).toHaveTextContent("20%");
  });

  it("renders an uninstrumented dimension as no data, never as 0%", async () => {
    renderWithAgent();
    const card = await screen.findByTestId("quality-card-discipline");
    expect(card).toHaveTextContent(enSelfEvolution.quality.measure.noData);
    expect(card).toHaveTextContent(enSelfEvolution.quality.measure.reason.not_instrumented);
    expect(card.textContent).not.toMatch(/\b0%/);
  });

  it("renders a below-floor dimension as sample-too-small with both counts", async () => {
    renderWithAgent();
    const card = await screen.findByTestId("quality-card-tool_failure_rate");
    expect(card).toHaveTextContent(enSelfEvolution.quality.measure.insufficient);
    expect(card).toHaveTextContent("3 of 10 needed");
    expect(card.textContent).not.toMatch(/\b0%/);
  });

  it("shows no degradation footer when every optional source is available", async () => {
    dashboardRef.current = dashboard({
      data_sources: {
        degraded: false,
        items: [
          { kind: "platform", available: true, required: true, degraded: false },
          { kind: "langfuse", available: true, required: false, degraded: false },
        ],
      },
    });
    renderWithAgent();
    await screen.findByTestId("quality-card-discipline");
    expect(screen.queryByTestId("degraded-langfuse")).not.toBeInTheDocument();
  });

  it("labels a disabled Langfuse export without withholding any card (T3)", async () => {
    dashboardRef.current = dashboard({
      data_sources: {
        degraded: true,
        items: [
          { kind: "platform", available: true, required: true, degraded: false },
          { kind: "langfuse", available: false, required: false, degraded: true },
        ],
      },
    });
    renderWithAgent();
    expect(await screen.findByTestId("degraded-langfuse")).toHaveTextContent(
      enSelfEvolution.quality.sources.langfuse,
    );
    // The seven cards are unchanged by the optional source being off.
    expect(screen.getByTestId("quality-card-retry_rate")).toHaveTextContent("20%");
  });

  it("states that D3 was never scored instead of showing a neutral band", async () => {
    renderWithAgent();
    expect(
      await screen.findByText(enSelfEvolution.quality.perplexity.empty.title),
    ).toBeInTheDocument();
  });

  it("keeps the two runtime profiles apart and never averages them", async () => {
    dashboardRef.current = dashboard({
      perplexity: [
        {
          version: 4,
          runtime_profile: "member",
          band: "low",
          percent_low: 5,
          percent_high: 15,
          evidence: [],
          model: "claude-opus-5",
          scored_at: "2026-09-20T10:00:00.000Z",
        },
        {
          version: 4,
          runtime_profile: "leader_task",
          band: "high",
          percent_low: 60,
          percent_high: 75,
          evidence: [],
          model: "claude-opus-5",
          scored_at: "2026-09-20T10:00:00.000Z",
        },
      ],
    });
    renderWithAgent();
    // The member profile is active first and only its band shows; the other
    // profile's band is behind the switch, and no combined band exists.
    expect(
      await screen.findByText(enSelfEvolution.quality.perplexity.profile.member),
    ).toBeInTheDocument();
    expect(
      screen.getByText(enSelfEvolution.quality.perplexity.profile.leader_task),
    ).toBeInTheDocument();
    expect(screen.getByText("5% — 15%")).toBeInTheDocument();
    expect(screen.queryByText("60% — 75%")).not.toBeInTheDocument();
  });

  it("says the window has no failed run rather than printing an empty list", async () => {
    dashboardRef.current = dashboard({
      window: { ...dashboard().window, failure_reasons: {}, excluded_failed_runs: 0 },
    });
    renderWithAgent();
    expect(await screen.findByText(enSelfEvolution.quality.reasons.empty)).toBeInTheDocument();
  });

  it("falls back to the empty dashboard shape without inventing zeroes", async () => {
    dashboardRef.current = {
      scope: "",
      scope_id: "",
      since: "",
      window: {
        version: 0,
        days: 0,
        runs: 0,
        measures: dashboard().window.measures,
        failure_reasons: {},
        excluded_failed_runs: 0,
      },
      versions: [],
      perplexity: [],
      data_sources: { degraded: false, items: [] },
    };
    renderWithAgent();
    expect(
      await screen.findByText(enSelfEvolution.quality.timeline.empty),
    ).toBeInTheDocument();
  });
});
