// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import type { SkillCatalogEntry, SkillSummary, SkillUsage } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { SkillsEvolutionBody } from "./skills-page";

/**
 * The skill-evolution list (RUYI-551 §2.4, fig 5): managed skills and
 * runtime discoveries are separate segments, a managed row navigates to the
 * detail route (the page holds no detail state of its own), and a discovery
 * imports through the existing runtime-local flow. Editing content stays on
 * the /skills management page.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const m = vi.hoisted(() => ({
  skills: [] as SkillSummary[],
  catalog: [] as SkillCatalogEntry[],
  usage: {} as Record<string, SkillUsage>,
  push: vi.fn(),
  importSkill: vi.fn(),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  skillListOptions: (wsId: string) => ({
    queryKey: ["skills", wsId],
    queryFn: () => Promise.resolve(m.skills),
  }),
  skillCatalogOptions: (wsId: string) => ({
    queryKey: ["skill-catalog", wsId],
    queryFn: () => Promise.resolve(m.catalog),
  }),
}));

vi.mock("@multica/core/self-evolution", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/self-evolution")>(
      "@multica/core/self-evolution",
    );
  return {
    ...actual,
    skillUsageOptions: (wsId: string, skillId: string) => ({
      queryKey: ["skill-usage", wsId, skillId],
      queryFn: () => Promise.resolve(m.usage[skillId] ?? null),
    }),
  };
});

vi.mock("../../skills/lib/use-catalog-skill-import", () => ({
  useCatalogSkillImport: () => ({ importSkill: m.importSkill, importingKey: null }),
}));

function makeAdapter(
  overrides: Partial<NavigationAdapter> = {},
): NavigationAdapter {
  return {
    push: m.push,
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/test-workspace/self-evolution/skills",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => p,
    ...overrides,
  };
}

function skillFixture(overrides: Partial<SkillSummary> = {}): SkillSummary {
  return {
    id: "skill-1",
    workspace_id: "ws-1",
    name: "code-review",
    description: "Reviews issues before completion",
    config: {},
    created_by: "u-1",
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
    ...overrides,
  };
}

function usageFixture(overrides: Partial<SkillUsage> = {}): SkillUsage {
  return {
    total: 9,
    last_30_days: 3,
    assigned_agents: 1,
    since: "2026-09-01T00:00:00Z",
    versions: [{ version: 4, count: 3 }],
    recent: [],
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
            <SkillsEvolutionBody wsId="ws-1" />
          </I18nProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  m.skills = [];
  m.catalog = [];
  m.usage = {};
  m.push.mockClear();
  m.importSkill.mockReset();
  m.importSkill.mockResolvedValue(undefined);
});

describe("SkillsEvolutionBody", () => {
  it("lists managed skills with 30-day usage and navigates a row to the detail route", async () => {
    m.skills = [skillFixture(), skillFixture({ id: "skill-2", name: "patent-draft" })];
    m.catalog = [
      { kind: "skill", name: "code-review", source: "workspace", id: "skill-1" },
      { kind: "skill", name: "patent-draft", source: "plugin", id: "skill-2" },
    ];
    m.usage = { "skill-1": usageFixture() };
    mount();

    const table = await screen.findByTestId("skills-managed-table");
    expect(within(table).getByText("code-review")).toBeTruthy();
    // Per-row usage queries resolve after the table itself — wait for them.
    await waitFor(() => expect(within(table).getByText("3 invocations")).toBeTruthy());
    expect(within(table).getByText("Version v4")).toBeTruthy();
    // A row without usage data shows an em dash, never a fabricated zero.
    await waitFor(() =>
      expect(
        within(screen.getByTestId("skills-row-skill-2")).getAllByText("—").length,
      ).toBeGreaterThan(0),
    );

    // The row is the click target: plain left click pushes the detail route.
    await userEvent.click(screen.getByTestId("skills-row-skill-1"));
    expect(m.push).toHaveBeenCalledWith("/test-workspace/self-evolution/skills/skill-1");
  });

  it("keeps runtime discoveries in their own segment and imports through the shared flow", async () => {
    m.skills = [skillFixture()];
    m.catalog = [
      { kind: "skill", name: "code-review", source: "workspace", id: "skill-1" },
      {
        kind: "discovery",
        name: "deploy-helper",
        source: "runtime",
        runtime_id: "rt-1",
        key: "deploy-helper",
        source_path: "/srv/agent/skills/deploy-helper",
      },
      {
        kind: "discovery",
        name: "code-review",
        source: "runtime",
        runtime_id: "rt-1",
        key: "code-review",
        matching_skill_id: "skill-1",
      },
    ];
    mount();

    await waitFor(() => expect(screen.getByTestId("skills-managed-table")).toBeTruthy());
    // Discoveries are not on the first screen.
    expect(screen.queryByTestId("skills-discoveries")).toBeNull();

    await userEvent.click(screen.getByRole("tab", { name: /Discoveries/ }));
    const section = screen.getByTestId("skills-discoveries");
    // Each sighting is one row; the one whose name collides with an authored
    // skill is marked "already covered" and carries no import button.
    const rows = Array.from(section.children) as HTMLElement[];
    const coveredRow = rows.find((r) => r.textContent?.includes("Already a skill"));
    expect(coveredRow?.textContent).toContain("code-review");
    expect(within(coveredRow!).queryByRole("button", { name: "Import" })).toBeNull();

    // The fresh sighting imports through the existing runtime-local flow.
    const freshRow = rows.find((r) => r.textContent?.includes("deploy-helper"))!;
    await userEvent.click(within(freshRow).getByRole("button", { name: "Import" }));
    expect(m.importSkill).toHaveBeenCalledWith(
      expect.objectContaining({ key: "deploy-helper", runtime_id: "rt-1" }),
    );
  });
});
