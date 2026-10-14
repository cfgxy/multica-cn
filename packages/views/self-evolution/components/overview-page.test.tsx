// @vitest-environment jsdom

import { it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import type { SelfEvolutionOverview } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { SelfEvolutionOverviewPage } from "./overview-page";

/**
 * The overview page's attention list (RUYI-551 §2.2 / walk #14).
 *
 * The daily retrospective (E3) is the leg that fills the proposal pool, so a
 * config that cannot run is an actionable row — not enabled, or enabled with
 * no executor agent — each carrying the navigation to the config page. A
 * configured (enabled + agent) retrospective stays silent: the list only
 * ever names actionable problems, and a cleared condition disappears.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  modelConfig: null as Record<string, unknown> | null,
  retrospectiveConfig: null as Record<string, unknown> | null,
  overview: null as SelfEvolutionOverview | null,
  retroFailure: null as Error | null,
}));

vi.mock("@multica/core/api", () => ({
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
  api: {
    getSelfEvolutionModelConfig: () => Promise.resolve(state.modelConfig),
    listSkillCatalog: () => Promise.resolve([]),
    getRetrospectiveConfig: () =>
      state.retroFailure
        ? Promise.reject(state.retroFailure)
        : Promise.resolve(state.retrospectiveConfig),
    getSelfEvolutionOverview: () => Promise.resolve(state.overview),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const MODEL_OK = {
  override: null,
  resolved: { status: "ok", source: "workspace" },
  scoring_enabled: false,
  encryption_ready: true,
};

function overviewFixture(): SelfEvolutionOverview {
  return {
    versions: [],
    quality: {
      since: "",
      days: 30,
      runs: 0,
      subjects_measured: 0,
      measures: {},
      excluded_failed_runs: 0,
    },
    quiz: {
      verdict: "insufficient",
      measured: false,
      required_sample: 12,
      required_baseline: 30,
    },
    knowledge: { dirs: 0, entries: 0 },
    skills: { count: 0, invocations: 0 },
    proposals: { total: 0, pending_owner: 0 },
  } as unknown as SelfEvolutionOverview;
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
            <SelfEvolutionOverviewPage />
          </I18nProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

afterEach(cleanup);

it("raises an actionable row when the retrospective is disabled or has no executor", async () => {
  state.modelConfig = MODEL_OK;
  state.overview = overviewFixture();

  // Disabled (or never saved — the server defaults every flag off).
  state.retrospectiveConfig = {
    enabled: false,
    include_in_review: false,
    window_days: 1,
    agent_id: "",
    agent_name: "",
  };
  const first = mount();
  await waitFor(() =>
    expect(screen.getByTestId("se-attention-list")).toBeTruthy(),
  );
  expect(screen.getByText("The daily retrospective is not enabled")).toBeTruthy();
  const link = screen.getByRole("link", { name: "Configure" }) as HTMLAnchorElement;
  expect(link.getAttribute("href")).toBe("/test-workspace/self-evolution/retrospective");
  first.unmount();

  // Enabled but executor-less: a run can still never start.
  state.retrospectiveConfig = {
    enabled: true,
    include_in_review: true,
    window_days: 1,
    agent_id: "",
    agent_name: "",
  };
  const second = mount();
  await waitFor(() =>
    expect(screen.getByText("The daily retrospective has no executor agent")).toBeTruthy(),
  );
  second.unmount();
});

it("stays silent once the retrospective is enabled with an executor agent", async () => {
  state.modelConfig = MODEL_OK;
  state.overview = overviewFixture();
  state.retrospectiveConfig = {
    enabled: true,
    include_in_review: true,
    window_days: 1,
    agent_id: "agent-1",
    agent_name: "executor",
  };
  mount();

  await waitFor(() => expect(screen.getByTestId("overview-proposals")).toBeTruthy());
  expect(screen.queryByTestId("se-attention-list")).toBeNull();
});

it("keeps the retrospective row out of the list when the config cannot be read", async () => {
  // The config read can fail (member-level endpoint, transient 500). The
  // attention list degrades to the other rows instead of lying about a
  // state it could not verify.
  state.modelConfig = MODEL_OK;
  state.overview = overviewFixture();
  state.retrospectiveConfig = null;
  state.retroFailure = new Error("boom");
  mount();

  await waitFor(() => expect(screen.getByTestId("overview-proposals")).toBeTruthy());
  expect(screen.queryByTestId("se-attention-list")).toBeNull();
});
