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
import jaCommon from "../../locales/ja/common.json";
import jaSelfEvolution from "../../locales/ja/self-evolution.json";
import koCommon from "../../locales/ko/common.json";
import koSelfEvolution from "../../locales/ko/self-evolution.json";
import { SE_NAV_GROUPS, SeSubNav } from "./se-sub-nav";

/**
 * The module's URL-driven left sub-navigation: six destinations in 3 groups.
 * What only a mount can show: the
 * group structure exists, counts ride the overview aggregate (no fake
 * zeros), and the detail route lights its list parent — /skills/<id>
 * belongs to Skill evolution, so a refresh on a detail page keeps its
 * section highlighted.
 */

const TEST_RESOURCES = {
  en: { common: enCommon, "self-evolution": enSelfEvolution },
  ja: { common: jaCommon, "self-evolution": jaSelfEvolution },
  ko: { common: koCommon, "self-evolution": koSelfEvolution },
};

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

function mount(
  pathname = "/test-workspace/self-evolution",
  locale: "en" | "ja" | "ko" = "en",
) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationProvider value={makeAdapter({ pathname })}>
          <I18nProvider locale={locale} resources={TEST_RESOURCES}>
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
  it("shows the six destinations in three groups with honest counts", async () => {
    mount();
    const nav = await screen.findByTestId("se-sub-nav");
    const links = Array.from(nav.querySelectorAll("a")).map((a) => a.textContent);
    // Overview first, then 4 groups × their items: knowledge, skills, quality,
    // quiz, proposals, versions, retrospective, config.
    expect(links).toEqual([
      "Overview",
      "Knowledge",
      "Skill evolution",
      "Quality & measures",
      "Clause governance",
      "Daily retrospective",
    ]);
    expect(screen.getByText("Knowledge assets")).toBeTruthy();
    expect(screen.getByText("Quality & governance")).toBeTruthy();
    expect(screen.getByText("Self-evolution workflow")).toBeTruthy();
    // Counts ride the overview aggregate, which resolves after the nav.
    await waitFor(() => expect(screen.getByText("2")).toBeTruthy()); // knowledge entries
    expect(screen.getByText("7")).toBeTruthy(); // skills
    expect(SE_NAV_GROUPS).toHaveLength(3);
  });

  it("does not add configuration as a sidebar destination", async () => {
    mount("/test-workspace/self-evolution/config");
    await screen.findByTestId("se-sub-nav");
    const active = Array.from(
      screen.getByTestId("se-sub-nav").querySelectorAll("a[data-active]"),
    ).map((a) => a.textContent);
    expect(active).toEqual([]);
  });

  it("highlights the consolidated quality and clause destinations for their tabs and details", async () => {
    const { unmount } = mount("/test-workspace/self-evolution/quality");
    await screen.findByTestId("se-sub-nav");
    expect(screen.getByRole("link", { name: "Quality & measures" }).hasAttribute("data-active")).toBe(true);
    unmount();

    mount("/test-workspace/self-evolution/clause-governance/clause-9");
    await screen.findByTestId("se-sub-nav");
    expect(screen.getByRole("link", { name: "Clause governance" }).hasAttribute("data-active")).toBe(true);
  });

  it("lights the skills list parent from the detail route", async () => {
    mount("/test-workspace/self-evolution/skills/skill-9");
    await screen.findByTestId("se-sub-nav");
    const active = Array.from(
      screen.getByTestId("se-sub-nav").querySelectorAll("a[data-active]"),
    ).map((a) => a.textContent);
    expect(active).toEqual(["Skill evolution"]);
  });

  it("labels the consolidated quality destination in Japanese and Korean", async () => {
    const { unmount: unmountJapanese } = mount("/test-workspace/self-evolution/quality", "ja");
    expect(await screen.findByRole("link", { name: "品質と計測" })).toBeTruthy();
    unmountJapanese();

    mount("/test-workspace/self-evolution/quality", "ko");
    expect(await screen.findByRole("link", { name: "품질 및 측정" })).toBeTruthy();
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
