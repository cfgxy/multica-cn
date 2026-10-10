// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import type {
  RetrospectiveConfig,
  RetrospectiveRun,
  SelfEvolutionModelConfig,
  SelfEvolutionOverview,
} from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { RetrospectivePage } from "./retrospective-page";

/**
 * The daily retrospective page (RUYI-551 §3.1) after RUYI-658: since RUYI-552
 * the retrospective runs as an execution-agent run and reads nothing from the
 * workspace model config, so the model-service card must be gone from here —
 * it lives on the module-config page alone. What only a page mount can show:
 * the card (and even its config query) is absent while the agent-driven tab
 * still renders. A regression re-adding the card fails both the testid check
 * and the zero-calls check, because the card fetches the model config itself.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  role: "owner" as string | null,
  config: null as RetrospectiveConfig | null,
  runs: [] as RetrospectiveRun[],
  agents: [] as unknown[],
  overview: null as SelfEvolutionOverview | null,
  modelConfig: null as SelfEvolutionModelConfig | null,
  modelConfigOptionsCalls: 0,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ userId: "u-1", role: state.role, member: null, isLoading: false }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({
    queryKey: ["test", "agents"],
    queryFn: () => Promise.resolve(state.agents),
  }),
}));

vi.mock("@multica/core/api", () => ({
  ApiError: class ApiError extends Error {},
  clientErrorMessage: () => undefined,
  api: {
    getRetrospectiveConfig: () => Promise.resolve(state.config),
    updateRetrospectiveConfig: () => Promise.resolve(state.config),
    listRetrospectiveRuns: () => Promise.resolve(state.runs),
    triggerRetrospectiveRun: () => Promise.resolve({}),
    getSelfEvolutionOverview: () => Promise.resolve(state.overview),
  },
}));

vi.mock("@multica/core/self-evolution", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/self-evolution")>(
      "@multica/core/self-evolution",
    );
  return {
    ...actual,
    selfEvolutionOverviewOptions: (wsId: string) => ({
      queryKey: ["self-evolution-overview", wsId],
      queryFn: () => Promise.resolve(state.overview),
    }),
    retrospectiveConfigOptions: (wsId: string) => ({
      queryKey: ["retrospective", wsId, "config"],
      queryFn: () => Promise.resolve(state.config),
    }),
    retrospectiveRunsOptions: (wsId: string) => ({
      queryKey: ["retrospective", wsId, "runs"],
      queryFn: () => Promise.resolve(state.runs),
    }),
    // The card is the only consumer of this config on this page; a mounted
    // card would both bump the counter and render the testid below.
    selfEvolutionModelConfigOptions: (wsId: string) => {
      state.modelConfigOptionsCalls += 1;
      return {
        queryKey: ["se-model-config", wsId],
        queryFn: () => Promise.resolve(state.modelConfig),
      };
    },
  };
});

function makeAdapter(
  overrides: Partial<NavigationAdapter> = {},
): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/test-workspace/self-evolution/retrospective",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => p,
    ...overrides,
  };
}

function overviewFixture(): SelfEvolutionOverview {
  return {
    versions: [],
    quality: { since: "", days: 30, runs: 0, subjects_measured: 0, measures: {}, excluded_failed_runs: 0 },
    quiz: null,
    knowledge: { dirs: 1, entries: 2, last_scan: null },
    skills: { count: 7, invocations: 12 },
  } as unknown as SelfEvolutionOverview;
}

function configFixture(): RetrospectiveConfig {
  return { enabled: true, include_in_review: false, window_days: 7, agent_id: null, agent_name: null };
}

function modelConfigFixture(): SelfEvolutionModelConfig {
  return {
    override: null,
    resolved: { status: "unconfigured", source: "" },
    scoring_enabled: true,
    encryption_ready: true,
  };
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationProvider value={makeAdapter()}>
          <I18nProvider locale="en" resources={TEST_RESOURCES}>
            <RetrospectivePage />
          </I18nProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  state.role = "owner";
  state.config = configFixture();
  state.runs = [];
  state.agents = [];
  state.overview = overviewFixture();
  // A resolved payload, not an error: if the card ever comes back it renders
  // fully instead of stopping at a skeleton, so the absence assertions below
  // cannot pass against a half-mounted card.
  state.modelConfig = modelConfigFixture();
  state.modelConfigOptionsCalls = 0;
});

describe("RetrospectivePage", () => {
  it("renders the agent-driven retrospective without the model-service card", async () => {
    renderPage();

    expect(await screen.findByTestId("retrospective-tab")).toBeTruthy();

    expect(screen.queryByTestId("model-config-card")).toBeNull();
    expect(state.modelConfigOptionsCalls).toBe(0);
  });
});
