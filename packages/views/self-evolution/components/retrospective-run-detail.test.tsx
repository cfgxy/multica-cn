// @vitest-environment jsdom

import { it, describe, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Issue, RetrospectiveRun } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { RetrospectiveRunDetailSheet, runStatsLine } from "./retrospective-run-detail";

/**
 * RUYI-742: the run detail sheet's issue lists follow the platform-common
 * issue-entry look (IssueChip: status icon + identifier + truncated title,
 * wrapped in an in-app link) instead of bare UUIDs, and the stats line no
 * longer contradicts the lists mid-run (scanned/analyzed derive from the
 * live detail membership; not-yet-known counters hold an em dash).
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const ID_1 = "11111111-1111-4111-8111-111111111111";
const ID_2 = "22222222-2222-4222-8222-222222222222";
const ID_MISSING = "33333333-3333-4333-8333-333333333333";

const state = vi.hoisted(() => ({
  /** Issues served from the workspace list cache (first page). */
  listIssues: [] as unknown[],
  /** issueId → detail issue, or an Error to reject the fallback fetch. */
  detailById: new Map<string, unknown>(),
}));

vi.mock("@multica/core/issues/queries", () => ({
  issueListOptions: () => ({
    queryKey: ["test", "issue-list"],
    queryFn: () => Promise.resolve(state.listIssues),
  }),
  issueDetailOptions: (_wsId: string, id: string) => ({
    queryKey: ["test", "issue-detail", id],
    queryFn: () => {
      const hit = state.detailById.get(id);
      return hit instanceof Error ? Promise.reject(hit) : Promise.resolve(hit ?? null);
    },
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({
    colorOf: (status: string) => (status === "custom_status" ? "#f97316" : null),
  }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/test-workspace/issues/${id}`,
  }),
}));

function issue(id: string, identifier: string, title: string): Issue {
  return { id, identifier, title, status: "done" } as Issue;
}

function runFixture(overrides: Partial<RetrospectiveRun> = {}): RetrospectiveRun {
  return {
    id: "run-1",
    status: "succeeded",
    trigger: "manual",
    window_start: "2026-10-12T00:00:00Z",
    window_end: "2026-10-13T00:00:00Z",
    issues_scanned: 5,
    issues_analyzed: 4,
    proposals_created: 2,
    proposals_merged: 1,
    duplicates_skipped: 3,
    error: "",
    detail: null,
    created_at: "2026-10-13T00:00:00Z",
    finished_at: "2026-10-13T00:30:00Z",
    task_id: null,
    ...overrides,
  };
}

function makeAdapter(overrides: Partial<NavigationAdapter> = {}): NavigationAdapter {
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

function renderSheet(run: RetrospectiveRun) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <NavigationProvider value={makeAdapter()}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <RetrospectiveRunDetailSheet
            run={run}
            agentId="agent-1"
            agentName="Alpha"
            onOpenChange={() => {}}
          />
        </I18nProvider>
      </NavigationProvider>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  state.listIssues = [];
  state.detailById.clear();
  cleanup();
});

describe("RetrospectiveRunDetailSheet issue lists (RUYI-742)", () => {
  it("renders resolved ids as platform-common entries: identifier + title behind the issue link", async () => {
    state.listIssues = [
      issue(ID_1, "RUY-101", "Fix the run detail issue list"),
      issue(ID_2, "RUY-102", "A very long retrospective run title that must truncate inside the narrow drawer column"),
    ];
    renderSheet(
      runFixture({ detail: { issue_ids: [ID_1, ID_2], analyzed_issue_ids: [ID_1] } }),
    );

    // An analyzed issue is also a scanned one: RUY-101 legitimately shows in
    // both lists, RUY-102 only in the scanned list.
    await screen.findAllByText("RUY-101");
    expect(screen.getAllByText("RUY-101")).toHaveLength(2);
    expect(screen.getAllByText("Fix the run detail issue list")).toHaveLength(2);
    expect(screen.getByText("RUY-102")).toBeInTheDocument();
    expect(
      screen.getByText("A very long retrospective run title that must truncate inside the narrow drawer column"),
    ).toHaveClass("truncate");

    expect(screen.getByTestId(`retrospective-issue-link-${ID_2}`).getAttribute("href")).toBe(
      `/test-workspace/issues/${ID_2}`,
    );
    const scannedAndAnalyzedLinks = screen.getAllByTestId(`retrospective-issue-link-${ID_1}`);
    expect(scannedAndAnalyzedLinks).toHaveLength(2);
    for (const link of scannedAndAnalyzedLinks) {
      expect(link.getAttribute("href")).toBe(`/test-workspace/issues/${ID_1}`);
    }
  });

  it("keeps the empty-list 「无」 slot and degrades an unresolvable id to its raw id", async () => {
    state.listIssues = [issue(ID_1, "RUY-101", "Fix the run detail issue list")];
    state.detailById.set(ID_MISSING, new Error("404"));
    renderSheet(
      runFixture({ detail: { issue_ids: [ID_MISSING], analyzed_issue_ids: [] } }),
    );

    // Unresolvable id keeps its slot inside the chip, as the raw id.
    await screen.findByText(ID_MISSING);
    // The empty analyzed list still renders the localized 「无」.
    expect(screen.getByText("None")).toBeInTheDocument();
    expect(screen.queryByText("RUY-101")).not.toBeInTheDocument();
  });

  it("resolves an id outside the list cache through the detail fallback fetch", async () => {
    state.listIssues = [];
    state.detailById.set(ID_1, issue(ID_1, "RUY-201", "Found via detail fetch"));
    renderSheet(runFixture({ detail: { issue_ids: [ID_1], analyzed_issue_ids: [] } }));

    await screen.findByText("RUY-201");
    expect(screen.getByText("Found via detail fetch")).toBeInTheDocument();
  });
});

describe("runStatsLine (RUYI-742)", () => {
  // Selector-resolving stand-in for the real `t`: runStatsLine's selectors
  // only read `$.retrospective.*` strings, which the en resource tree serves.
  const enT = ((selector: (tree: never) => unknown) =>
    selector(enSelfEvolution as never)) as unknown as Parameters<
    typeof runStatsLine
  >[1];

  it("derives scanned/analyzed from live detail membership mid-run and holds unknown counters at an em dash", () => {
    const line = runStatsLine(
      runFixture({
        status: "running",
        finished_at: null,
        issues_scanned: 0,
        issues_analyzed: 0,
        detail: { issue_ids: [ID_1, ID_2], analyzed_issue_ids: [] },
      }),
      enT,
    );
    expect(line).toBe(
      "Scanned 2 · Analyzed 0 · Created — · Merged — · Duplicates skipped —",
    );
  });

  it("reads the authoritative columns for a terminal run", () => {
    const line = runStatsLine(runFixture(), enT);
    expect(line).toBe("Scanned 5 · Analyzed 4 · Created 2 · Merged 1 · Duplicates skipped 3");
  });
});
