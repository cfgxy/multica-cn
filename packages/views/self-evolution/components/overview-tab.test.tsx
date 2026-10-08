// @vitest-environment jsdom

import { it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import type { SelfEvolutionOverview } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import jaSelfEvolution from "../../locales/ja/self-evolution.json";
import koSelfEvolution from "../../locales/ko/self-evolution.json";
import zhSelfEvolution from "../../locales/zh-Hans/self-evolution.json";
import { OverviewTab } from "./overview-tab";

/**
 * The overview tab's wiring (RUYI-284).
 *
 * The server holds the aggregate honest (its handler tests do the counting);
 * what only a mount can show is asserted here: the three kinds of emptiness
 * stay distinguishable on screen — a plane with nothing says so, a sample
 * below the floor shows the marker instead of a number, and a failed fetch is
 * an error state with a retry — and the tab offers no write control at all.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  overview: null as SelfEvolutionOverview | null,
  failure: null as Error | null,
  calls: 0,
}));

vi.mock("@multica/core/api", () => ({
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
  api: {
    getSelfEvolutionOverview: () => {
      state.calls += 1;
      if (state.failure) return Promise.reject(state.failure);
      return Promise.resolve(state.overview);
    },
  },
}));

function overviewFixture(overrides: Partial<SelfEvolutionOverview> = {}): SelfEvolutionOverview {
  return {
    versions: [
      {
        scope: "workspace",
        version_count: 3,
        subject_count: 1,
        current_version: 3,
        last_change_at: "2026-09-30T01:00:00Z",
        last_actor: "Gu Xiaoyu",
      },
      {
        scope: "agent",
        version_count: 5,
        subject_count: 2,
        last_change_at: "2026-09-29T01:00:00Z",
      },
    ],
    quality: {
      since: "2026-09-01",
      days: 30,
      runs: 19,
      subjects_measured: 2,
      measures: {
        injected_tokens: { state: "ok", unit: "count", value: 1200, numerator: null, denominator: null, sample: 19, threshold: 5 },
        run_tokens_median: { state: "ok", unit: "count", value: 3400, numerator: null, denominator: null, sample: 19, threshold: 5 },
        discipline: { state: "ok", unit: "score", value: 97, score_max: 100, numerator: null, denominator: null, sample: 19, threshold: 5 },
        tool_failure_rate: { state: "ok", unit: "ratio", value: 0.08, numerator: 4, denominator: 50, sample: 50, threshold: 30 },
        retry_rate: { state: "insufficient_sample", unit: "ratio", value: null, numerator: 1, denominator: 3, sample: 3, threshold: 5 },
        failure_attribution: { state: "no_data", unit: "count", value: null, numerator: null, denominator: null, sample: 0, threshold: 0 },
        first_pass_rate: { state: "ok", unit: "ratio", value: 0.9, numerator: 9, denominator: 10, sample: 10, threshold: 5 },
      },
      excluded_failed_runs: 2,
    },
    quiz: {
      scope_id: "agent-1",
      scope_name: "alpha",
      current_version: 2,
      baseline_version: 1,
      verdict: "insufficient",
      measured: true,
      required_sample: 12,
      required_baseline: 30,
      last_measured_at: "2026-09-30T02:00:00Z",
    },
    knowledge: {
      dirs: 1,
      entries: 2,
      last_scan: { result: "changed", trigger_source: "manual", started_at: "2026-09-30T04:00:00Z" },
    },
    skills: { count: 2, invocations: 7 },
    ...overrides,
  };
}

function makeAdapter(
  overrides: Partial<NavigationAdapter> = {},
): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/test-workspace/self-evolution",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => p,
    ...overrides,
  };
}

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationProvider value={makeAdapter()}>
          <I18nProvider locale="en" resources={TEST_RESOURCES}>
            <OverviewTab wsId="ws-1" />
          </I18nProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  state.overview = null;
  state.failure = null;
  state.calls = 0;
});

it("renders the six sections from the aggregate", async () => {
  state.overview = overviewFixture();
  mount();

  await waitFor(() => expect(screen.getByTestId("overview-tier-workspace")).toBeTruthy());
  expect(screen.getByText("Current v3")).toBeTruthy();
  // Two agent subjects: the highest version must not read as a fleet state.
  expect(screen.getByText("2 subjects advancing on their own")).toBeTruthy();
  expect(screen.getByText("5 versions across 2 subjects")).toBeTruthy();

  // Quality: the window line and one measured chip, the excluded note.
  expect(screen.getByText("Quality readings (last 30 days)")).toBeTruthy();
  expect(screen.getByText("19 runs · 2 agents with readings")).toBeTruthy();
  expect(screen.getByTestId("overview-measure-tool_failure_rate").textContent).toContain("8%");
  expect(
    screen.getByText("2 runs excluded for environment or provider failures"),
  ).toBeTruthy();

  // Quiz: latest measured scope with the below-floor marker, not a verdict.
  expect(screen.getByTestId("overview-quiz").textContent).toContain("alpha");
  expect(screen.getByText("Baseline v1 → v2")).toBeTruthy();
  expect(
    screen.getByText("Insufficient sample (baseline needs 30, current needs 12 usable readings)"),
  ).toBeTruthy();

  // The proposals slot stays but is deliberately unwired (RUYI-305): an
  // explicit pending note, never a count of the old model's rows.
  expect(screen.getByTestId("overview-proposals").textContent).toContain(
    "Proposal metrics arrive with the new proposal model",
  );
  expect(screen.getByTestId("overview-knowledge").textContent).toContain("1 directories");
  expect(screen.getByTestId("overview-skills").textContent).toContain("7 explicit invocations in total");
});

it("keeps the three kinds of emptiness distinguishable and offers no write control", async () => {
  state.overview = overviewFixture({
    versions: [],
    quality: {
      since: "",
      days: 30,
      runs: 0,
      subjects_measured: 0,
      measures: {
        injected_tokens: { state: "no_data", value: null, numerator: null, denominator: null, sample: 0, threshold: 0 },
        run_tokens_median: { state: "no_data", value: null, numerator: null, denominator: null, sample: 0, threshold: 0 },
        discipline: { state: "no_data", value: null, numerator: null, denominator: null, sample: 0, threshold: 0 },
        tool_failure_rate: { state: "no_data", value: null, numerator: null, denominator: null, sample: 0, threshold: 0 },
        retry_rate: { state: "no_data", value: null, numerator: null, denominator: null, sample: 0, threshold: 0 },
        failure_attribution: { state: "no_data", value: null, numerator: null, denominator: null, sample: 0, threshold: 0 },
        first_pass_rate: { state: "no_data", value: null, numerator: null, denominator: null, sample: 0, threshold: 0 },
      },
      excluded_failed_runs: 0,
    },
    quiz: { verdict: "insufficient", measured: false, required_sample: 12, required_baseline: 30 },
    knowledge: { dirs: 0, entries: 0 },
    skills: { count: 0, invocations: 0 },
  });
  mount();

  await waitFor(() => expect(screen.getByTestId("overview-versions")).toBeTruthy());
  expect(screen.getByText("No version history on any tier yet.")).toBeTruthy();
  expect(screen.getByText("No measured runs in this window.")).toBeTruthy();
  expect(screen.getByText("No recorded measurements yet.")).toBeTruthy();
  expect(screen.getByText("No registered directories yet.")).toBeTruthy();
  expect(screen.getByText("No registered skills yet.")).toBeTruthy();

  // A lens, not a surface: the only button this tab can ever render is the
  // error state's retry, and the happy path renders none.
  expect(document.querySelectorAll("button")).toHaveLength(0);
});

it("shows a failed fetch as an explicit error with a working retry", async () => {
  state.failure = new Error("boom");
  mount();

  await waitFor(() => expect(screen.getByTestId("overview-error")).toBeTruthy());
  expect(screen.getByRole("alert")).toBeTruthy();

  state.failure = null;
  state.overview = overviewFixture();
  screen.getByRole("button", { name: "Retry" }).click();
  await waitFor(() => expect(screen.getByTestId("overview-versions")).toBeTruthy());
  expect(state.calls).toBeGreaterThanOrEqual(2);
});

it("keeps the four locale files key-for-key identical", () => {
  // ICU cardinal plurals split one key into per-rule variants, and only en
  // ever needs "_one" — zh/ja/ko cardinals are all "other" (the pre-existing
  // skills block already ships that asymmetry). Parity is over plural
  // families, so suffixes are folded away before comparing.
  const keysOf = (obj: unknown, prefix = ""): string[] => {
    if (typeof obj !== "object" || obj === null) return [prefix];
    return Object.entries(obj as Record<string, unknown>).flatMap(([k, v]) => {
      const name = k.replace(/_(one|two|few|many|other)$/, "");
      return keysOf(v, prefix === "" ? name : `${prefix}.${name}`);
    });
  };
  const en = new Set(keysOf(enSelfEvolution));
  for (const other of [jaSelfEvolution, koSelfEvolution, zhSelfEvolution]) {
    const keys = new Set(keysOf(other));
    expect([...en].filter((k) => !keys.has(k))).toEqual([]);
    expect([...keys].filter((k) => !en.has(k))).toEqual([]);
  }
});
