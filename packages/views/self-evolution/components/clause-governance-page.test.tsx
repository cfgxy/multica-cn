// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useMemo, useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import type { NavigationAdapter } from "../../navigation";
import { NavigationProvider } from "../../navigation";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { ClauseGovernanceDetailPage, ClauseGovernancePage } from "./clause-governance-page";

const resources = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const m = vi.hoisted(() => ({
  overview: null as Record<string, unknown> | null,
  reconciliation: null as Record<string, unknown> | null,
  detail: null as Record<string, unknown> | null,
  overviewOptions: vi.fn(),
  reconciliationOptions: vi.fn(),
  detailOptions: vi.fn(),
  overviewQuery: vi.fn(),
  reconciliationQuery: vi.fn(),
  detailQuery: vi.fn(),
  replace: vi.fn(),
  push: vi.fn(),
  updateClauseContent: vi.fn(),
  deactivateClause: vi.fn(),
  deleteClause: vi.fn(),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/api", () => ({ api: {
  updateClauseContent: m.updateClauseContent,
  deactivateClause: m.deactivateClause,
  deleteClause: m.deleteClause,
} }));
vi.mock("@multica/core/self-evolution", () => ({
  clauseGovernanceOverviewOptions: (wsId: string, days: number) => {
    m.overviewOptions(wsId, days);
    return { queryKey: ["clause-governance", "overview", wsId, days], queryFn: () => m.overviewQuery(wsId, days) };
  },
  clauseGovernanceReconciliationOptions: (wsId: string, days: number) => {
    m.reconciliationOptions(wsId, days);
    return { queryKey: ["clause-governance", "reconciliation", wsId, days], queryFn: () => m.reconciliationQuery(wsId, days) };
  },
  clauseGovernanceDetailOptions: (wsId: string, clauseId: string, days: number) => {
    m.detailOptions(wsId, clauseId, days);
    return { queryKey: ["clause-governance", "detail", wsId, clauseId, days], queryFn: () => m.detailQuery(wsId, clauseId, days) };
  },
}));
vi.mock("./self-evolution-shell", () => ({ SelfEvolutionShell: ({ children }: { children: ReactNode }) => children }));

function makeAdapter(search: string, onReplace: (path: string) => void): NavigationAdapter {
  return {
    push: m.push,
    replace: (path) => {
      m.replace(path);
      window.history.replaceState({}, "", path);
      onReplace(path);
    },
    back: vi.fn(),
    pathname: "/test-workspace/self-evolution/clause-governance",
    searchParams: new URLSearchParams(search),
    hash: "",
    getShareableUrl: (path) => path,
  };
}

function NavigationHarness({ children, initialSearch }: { children: ReactNode; initialSearch: string }) {
  const [search, setSearch] = useState(initialSearch);
  const adapter = useMemo(() => makeAdapter(search, (path) => {
    setSearch(path.includes("?") ? path.slice(path.indexOf("?")) : "");
  }), [search]);
  return <NavigationProvider value={adapter}>{children}</NavigationProvider>;
}

function mount(element: ReactNode, search = "?days=30") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rendered = render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationHarness initialSearch={search}>
          <I18nProvider locale="en" resources={resources}>{element}</I18nProvider>
        </NavigationHarness>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
  return { ...rendered, client };
}

function overviewFixture() {
  return {
    workspace_id: "ws-1",
    since: "2026-10-01T00:00:00Z",
    dynamic_injection_disabled: false,
    clauses: [{ id: "clause-1", slug: "sample-clause", title: "Sample clause", status: "active", latest_version: 2, injections: 5, runs: 4, injected_tokens: 0, tokens_pending: true }],
    status_counts: { active: 1, deprecated: 0, revoked: 0 },
    totals: { injections: 5, runs: 4, degraded_rows: 0, degraded_runs: 0, clause_tokens: 0 },
    migration: { total_carriers: 2, bound_carriers: 1, by_scope: { workspace: 1 }, by_scope_total: { workspace: 2 } },
    rollback_suggestions: [],
  };
}

function reconciliationFixture() {
  return {
    since: "2026-10-01T00:00:00Z",
    healthy: false,
    layer_set_violations: [],
    totals: { injections: 0, runs: 0, degraded_rows: 0, degraded_runs: 0, clause_tokens: 0 },
    drift: [{ scope: "workspace", scope_id: "scope-1", scope_name: "Production workspace", entries: [{ kind: "stale_rev", slug: "sample-clause", marker_rev: 1, expected_rev: 2, carrier_content: "PRIVATE CARRIER TEXT" }] }],
    carriers_scanned: 1,
  };
}

function detailFixture() {
  return {
    id: "clause-1",
    slug: "sample-clause",
    title: "Sample clause",
    status: "active",
    latest_version: 2,
    versions: [
      { version: 1, content: "First version", content_sha256: "a".repeat(64), source: "proposal", carrier_scope: "workspace", created_by_type: "member", created_at: "2026-10-01T00:00:00Z" },
      { version: 2, content: "Second version", content_sha256: "b".repeat(64), source: "proposal", carrier_scope: "workspace", created_by_type: "member", created_at: "2026-10-02T00:00:00Z" },
    ],
    bindings: [
      { id: "binding-all", scope: "workspace", scope_id: "ws-1", status: "active", conditions: null, since_clause_version: 1, created_at: "2026-10-01T00:00:00Z" },
      { id: "binding-filtered", scope: "project", scope_id: "project-1", status: "active", conditions: { task_kinds: ["coding", "review"] }, since_clause_version: 2, created_at: "2026-10-02T00:00:00Z" },
    ],
    proposals: [],
    quality_daily: [
      { day: "2026-10-01", clause_version: 1, finished_runs: 28, attributable_failed_runs: 2, excluded_failed_runs: 50, injected_tokens: null, run_tokens_median: null },
      { day: "2026-10-02", clause_version: 2, finished_runs: 10, attributable_failed_runs: 19, excluded_failed_runs: 1, injected_tokens: null, run_tokens_median: null },
    ],
    injections: 0,
    runs: 0,
    injected_tokens: 0,
  };
}

beforeEach(() => {
  m.overview = overviewFixture();
  m.reconciliation = reconciliationFixture();
  m.detail = detailFixture();
  m.overviewOptions.mockClear();
  m.reconciliationOptions.mockClear();
  m.detailOptions.mockClear();
  m.overviewQuery.mockReset().mockImplementation(() => Promise.resolve(m.overview));
  m.reconciliationQuery.mockReset().mockImplementation(() => Promise.resolve(m.reconciliation));
  m.detailQuery.mockReset().mockImplementation(() => Promise.resolve(m.detail));
  m.replace.mockClear();
  m.push.mockClear();
  m.updateClauseContent.mockReset().mockResolvedValue(undefined);
  m.deactivateClause.mockReset().mockResolvedValue(undefined);
  m.deleteClause.mockReset().mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("ClauseGovernancePage", () => {
  it("writes the selected window to the URL, scopes/refetches both queries, and keeps carrier text out of alerts", async () => {
    mount(<ClauseGovernancePage />, "?days=30&tab=overview");
    await screen.findByText("Sample clause");
    expect(screen.getByText("Production workspace")).toBeTruthy();
    expect(screen.getByRole("link", { name: "sample-clause · Sample clause" })).toBeTruthy();

    expect(m.overviewOptions).toHaveBeenCalledWith("ws-1", 30);
    expect(m.reconciliationOptions).toHaveBeenCalledWith("ws-1", 30);
    expect(screen.getByText("Pending")).toBeTruthy();
    expect(screen.queryByText("PRIVATE CARRIER TEXT")).toBeNull();

    fireEvent.change(screen.getByLabelText("Stats window"), { target: { value: "90" } });
    await waitFor(() => {
      expect(m.replace).toHaveBeenCalledWith("/test-workspace/self-evolution/clause-governance?days=90&tab=overview");
      expect(m.overviewOptions).toHaveBeenCalledWith("ws-1", 90);
      expect(m.reconciliationOptions).toHaveBeenCalledWith("ws-1", 90);
      expect(m.overviewQuery).toHaveBeenCalledWith("ws-1", 90);
      expect(m.reconciliationQuery).toHaveBeenCalledWith("ws-1", 90);
    });

    const refreshButton = screen.getByRole("button", { name: "Refresh" });
    await waitFor(() => expect(refreshButton.hasAttribute("disabled")).toBe(false));
    const overviewCalls = m.overviewQuery.mock.calls.length;
    const reconciliationCalls = m.reconciliationQuery.mock.calls.length;
    fireEvent.click(refreshButton);
    await waitFor(() => {
      expect(m.overviewQuery.mock.calls.length).toBeGreaterThan(overviewCalls);
      expect(m.reconciliationQuery.mock.calls.length).toBeGreaterThan(reconciliationCalls);
    });
  });
});

describe("ClauseGovernanceDetailPage", () => {
  it("counts attributable failures in the 30-run threshold and renders null metrics as unmeasured", async () => {
    mount(<ClauseGovernanceDetailPage clauseId="clause-1" />);
    await screen.findByRole("heading", { name: "Sample clause" });
    expect(m.detailOptions).toHaveBeenCalledWith("ws-1", "clause-1", 30);

    const versionSelects = screen.getAllByRole("combobox");
    expect(versionSelects).toHaveLength(2);
    fireEvent.change(versionSelects[0]!, { target: { value: "1" } });
    fireEvent.change(versionSelects[1]!, { target: { value: "2" } });
    const compareButton = screen.getByRole("button", { name: "Compare versions" });
    expect(compareButton.hasAttribute("disabled")).toBe(false);
    fireEvent.click(compareButton);
    const compareDialog = await screen.findByRole("dialog", { name: "Compare v1 ↔ v2" });
    expect(compareDialog.querySelectorAll("pre")).toHaveLength(2);
    expect(compareDialog.textContent).toContain("First version");
    expect(compareDialog.textContent).toContain("Second version");
    expect(screen.getByRole("table", { name: "Compare v1 ↔ v2" })).toBeTruthy();
    expect(compareDialog.textContent).toContain("Attributable runs");
    const comparisonRows = compareDialog.querySelectorAll("table tbody tr");
    expect(comparisonRows).toHaveLength(2);
    expect(comparisonRows[0]?.textContent).toContain("V1");
    expect(comparisonRows[0]?.textContent).toContain("30");
    expect(comparisonRows[0]?.textContent).toContain("2");
    expect(comparisonRows[0]?.textContent).toContain("Unmeasured");
    expect(comparisonRows[1]?.textContent).toContain("V2");
    expect(comparisonRows[1]?.textContent).toContain("29");
    expect(comparisonRows[1]?.textContent).toContain("19");
    expect(comparisonRows[1]?.textContent).toContain("Unmeasured");
    expect(compareDialog.textContent).toContain("Insufficient sample — no percentage conclusions");
    expect(compareDialog.textContent).not.toContain("%");
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    fireEvent.click(screen.getByRole("tab", { name: "Quality & injection" }));
    expect(await screen.findByRole("img", { name: /Finished runs and attributable failures/ })).toBeTruthy();
    expect(screen.getAllByText("Unmeasured").length).toBeGreaterThanOrEqual(2);
    expect(screen.queryByText(/^0$/)).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: "Bindings" }));
    expect(screen.getByText("All task kinds")).toBeTruthy();
    expect(screen.getByText("coding")).toBeTruthy();
    expect(screen.getByText("review")).toBeTruthy();
  });

  it("saves direct edits as a new clause version", async () => {
    mount(<ClauseGovernanceDetailPage clauseId="clause-1" />);
    await screen.findByRole("heading", { name: "Sample clause" });

    fireEvent.click(screen.getByRole("button", { name: "Edit content" }));
    const editor = screen.getByRole("textbox", { name: "Clause content" });
    fireEvent.change(editor, { target: { value: "Updated clause text" } });
    fireEvent.click(screen.getByRole("button", { name: "Save new version" }));

    await waitFor(() => expect(m.updateClauseContent).toHaveBeenCalledWith("clause-1", "Updated clause text", 2));
    await waitFor(() => expect(screen.queryByRole("textbox", { name: "Clause content" })).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("preserves an unsaved draft when detail data is refetched", async () => {
    const mounted = mount(<ClauseGovernanceDetailPage clauseId="clause-1" />);
    await screen.findByRole("heading", { name: "Sample clause" });

    fireEvent.click(screen.getByRole("button", { name: "Edit content" }));
    const editor = screen.getByRole("textbox", { name: "Clause content" });
    fireEvent.change(editor, { target: { value: "Unsaved draft" } });
    m.detail = {
      ...detailFixture(),
      latest_version: 3,
      versions: [...detailFixture().versions, {
        version: 3,
        content: "Server-side update",
        content_sha256: "c".repeat(64),
        source: "manual",
        carrier_scope: "workspace",
        created_by_type: "member",
        created_at: "2026-10-03T00:00:00Z",
      }],
    };

    await mounted.client.invalidateQueries({
      queryKey: ["clause-governance", "detail", "ws-1", "clause-1", 30],
    });
    await waitFor(() => expect(m.detailQuery).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("textbox", { name: "Clause content" })).toHaveValue("Unsaved draft");
  });

  it("requires confirmation before deactivating or deleting and reports failures", async () => {
    m.deactivateClause.mockRejectedValueOnce(new Error("forbidden"));
    mount(<ClauseGovernanceDetailPage clauseId="clause-1" />);
    await screen.findByRole("heading", { name: "Sample clause" });

    fireEvent.click(screen.getByRole("button", { name: "Deactivate" }));
    const deactivateDialog = await screen.findByRole("alertdialog");
    expect(within(deactivateDialog).getByText(/withdraws the active binding/i)).toBeTruthy();
    fireEvent.click(within(deactivateDialog).getByRole("button", { name: "Deactivate" }));
    await waitFor(() => expect(m.deactivateClause).toHaveBeenCalledWith("clause-1"));
    expect(await screen.findByRole("alert")).toHaveTextContent("The operation failed. Check your permissions and try again.");

    fireEvent.click(screen.getByRole("button", { name: "Delete clause" }));
    const deleteDialog = await screen.findByRole("alertdialog");
    expect(within(deleteDialog).getByText(/permanently deletes the clause/i)).toBeTruthy();
    fireEvent.click(within(deleteDialog).getByRole("button", { name: "Delete clause" }));
    await waitFor(() => expect(m.deleteClause).toHaveBeenCalledWith("clause-1"));
    await waitFor(() => expect(m.push).toHaveBeenCalledWith("/test-workspace/self-evolution/clause-governance?days=30"));
  });

  it("keeps deprecated clauses editable and deletable without offering deactivation", async () => {
    m.detail = { ...detailFixture(), status: "deprecated" };
    mount(<ClauseGovernanceDetailPage clauseId="clause-1" />);
    await screen.findByRole("heading", { name: "Sample clause" });

    expect(screen.getByRole("button", { name: "Edit content" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete clause" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Deactivate" })).toBeNull();
  });

  it("does not offer direct editing for revoked clauses", async () => {
    m.detail = { ...detailFixture(), status: "revoked" };
    mount(<ClauseGovernanceDetailPage clauseId="clause-1" />);
    await screen.findByRole("heading", { name: "Sample clause" });

    expect(screen.queryByRole("button", { name: "Edit content" })).toBeNull();
    expect(screen.getByRole("button", { name: "Delete clause" })).toBeTruthy();
  });
});
