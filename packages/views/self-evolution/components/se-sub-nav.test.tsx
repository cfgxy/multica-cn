// @vitest-environment jsdom

import { it, expect, vi, beforeEach, describe } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import type { SelfEvolutionOverview } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { SE_NAV_GROUPS, SeSubNav } from "./se-sub-nav";

/**
 * The module's URL-driven left sub-navigation (RUYI-551 §2.1): 9 pages in
 * 4 groups plus Overview, one route each. What only a mount can show: the
 * group structure exists, counts ride the overview aggregate (no fake
 * zeros), and the detail route lights its list parent — /skills/<id>
 * belongs to Skill evolution, so a refresh on a detail page keeps its
 * section highlighted.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  overview: null as SelfEvolutionOverview | null,
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
  };
});

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
    quiz: null,
    knowledge: {
      dirs: 1,
      entries: 2,
      last_scan: null,
    },
    skills: { count: 7, invocations: 12 },
  } as unknown as SelfEvolutionOverview;
}

function mount(pathname = "/test-workspace/self-evolution") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationProvider value={makeAdapter({ pathname })}>
          <I18nProvider locale="en" resources={TEST_RESOURCES}>
            <SeSubNav wsId="ws-1" />
          </I18nProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  state.overview = overviewFixture();
});

describe("SeSubNav", () => {
  it("shows Overview, all 4 groups with their items, and honest counts", async () => {
    mount();
    const nav = await screen.findByTestId("se-sub-nav");
    const links = Array.from(nav.querySelectorAll("a")).map((a) => a.textContent);
    // Overview first, then 4 groups × their items: knowledge, skills, quality,
    // quiz, proposals, versions, retrospective, config.
    expect(links).toEqual([
      "Overview",
      "Knowledge",
      "Skill evolution",
      "Quality",
      "Quiz",
      "Proposals",
      "Versions",
      "Daily retrospective",
      "Module config",
    ]);
    // The group headings sit between the items.
    expect(screen.getByText("Knowledge assets")).toBeTruthy();
    expect(screen.getByText("Measures")).toBeTruthy();
    expect(screen.getByText("Evolution loop")).toBeTruthy();
    expect(screen.getByText("Configuration")).toBeTruthy();
    // Counts ride the overview aggregate, which resolves after the nav.
    await waitFor(() => expect(screen.getByText("2")).toBeTruthy()); // knowledge entries
    expect(screen.getByText("7")).toBeTruthy(); // skills
    expect(SE_NAV_GROUPS).toHaveLength(4);
  });

  it("marks only the current page active", async () => {
    mount("/test-workspace/self-evolution/config");
    await screen.findByTestId("se-sub-nav");
    const active = Array.from(
      screen.getByTestId("se-sub-nav").querySelectorAll("a[data-active]"),
    ).map((a) => a.textContent);
    expect(active).toEqual(["Module config"]);
  });

  it("lights the skills list parent from the detail route", async () => {
    mount("/test-workspace/self-evolution/skills/skill-9");
    await screen.findByTestId("se-sub-nav");
    const active = Array.from(
      screen.getByTestId("se-sub-nav").querySelectorAll("a[data-active]"),
    ).map((a) => a.textContent);
    expect(active).toEqual(["Skill evolution"]);
  });

  it("renders without a badge while the aggregate is still loading", async () => {
    state.overview = null;
    mount();
    await waitFor(() => expect(screen.getByTestId("se-sub-nav")).toBeTruthy());
    // No fabricated zeros in the nav while the overview loads.
    expect(screen.queryByText("0")).toBeNull();
    expect(screen.getByRole("link", { name: "Knowledge" })).toBeTruthy();
  });
});
