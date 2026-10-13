// @vitest-environment jsdom

import { it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { SelfEvolutionShell } from "./self-evolution-shell";

/**
 * The shell body wrapper is the module's scroll container (RUYI-745): it
 * must carry the bounded-flex + overflow classes so tall page content (the
 * skills page alone can hold 100+ discovery rows) scrolls inside the canvas
 * instead of stretching past it and getting clipped with no scrollbar. The
 * parent chain above this shell is a bounded flex column on both platforms
 * (web SidebarInset, desktop MainCanvas), so the contract lives entirely in
 * these classes.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const adapter: NavigationAdapter = {
  push: vi.fn(),
  replace: vi.fn(),
  back: vi.fn(),
  pathname: "/test-workspace/self-evolution/skills",
  searchParams: new URLSearchParams(),
  hash: "",
  getShareableUrl: (p) => p,
};

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationProvider value={adapter}>
          <I18nProvider locale="en" resources={TEST_RESOURCES}>
            <SelfEvolutionShell>
              <div data-testid="se-page-body">page body</div>
            </SelfEvolutionShell>
          </I18nProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

it("body wrapper is the bounded scroll container for page content", () => {
  const { getByTestId } = mount();
  const wrapper = getByTestId("se-page-body").parentElement;
  expect(wrapper).toHaveClass("overflow-y-auto", "min-h-0", "flex-1", "min-w-0");
});

it("shell root is height-bounded so the scroll container has a ceiling", () => {
  const { getByTestId } = mount();
  const root = getByTestId("se-page-body").parentElement?.parentElement
    ?.parentElement;
  expect(root).toHaveClass("h-full", "min-h-0", "flex-col");
});
