// @vitest-environment jsdom

import { it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, fireEvent, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import type { PromptProposal, PromptProposalPreview, RetrospectiveRun } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import {
  NavigationProvider,
  type NavigationAdapter,
} from "../../navigation";
import { ProposalTab } from "./proposal-tab";

/**
 * The legislation tab's wiring (RUYI-305 E2, loop-first narrative RUYI-551).
 *
 * The state machine, permissions and gate semantics are enforced by the
 * server and covered by its handler tests. What only a mount can show is
 * asserted here: the owner-only lifecycle buttons appear exactly for the
 * statuses they are legal on, a non-owner sees no lifecycle controls at all
 * while creation stays available, and — the injection defense — the approve
 * button stays disabled until the full-text diff preview has rendered and
 * the reviewer ticked the confirmation. RUYI-551 adds the loop-first page
 * header, the source filter (manual / daily retrospective / merged) and the
 * empty-pool guidance link to the retrospective config.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  role: "owner" as string | null,
  proposals: [] as PromptProposal[],
  approved: [] as { id: string; confirmDiffPreviewed: boolean }[],
  batchApproved: [] as { ids: string[]; confirmDiffPreviewed: boolean }[],
  retroRuns: [] as RetrospectiveRun[],
  proposalCalls: [] as { status?: string; runId?: string }[],
}));

// The chip shell is mocked: this suite asserts the anchor *linking* (href +
// degrade), which the tab owns — the chip's own resolution behavior is
// covered by its own suite.
vi.mock("../../issues/components/issue-chip", () => ({
  IssueChip: ({ issueId }: { issueId: string }) => (
    <span data-testid={`issue-chip-${issueId}`}>{issueId.slice(0, 8)}</span>
  ),
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ userId: "u-1", role: state.role, member: null, isLoading: false }),
}));

vi.mock("@multica/core/api", () => ({
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
  api: {
    listPromptProposals: (params?: { status?: string; runId?: string }) => {
      state.proposalCalls.push(params ?? {});
      return Promise.resolve(state.proposals);
    },
    createPromptProposal: vi.fn(),
    updatePromptProposalDraft: vi.fn(),
    submitPromptProposal: vi.fn(),
    previewPromptProposal: (id: string) =>
      Promise.resolve({
        proposal: state.proposals.find((p) => p.id === id),
        diff: [
          { kind: "context", text: "# Workspace Context" },
          { kind: "add", text: "- **每日同步**：开工前回报当日计划。" },
        ],
        current_sha256: "abc",
        baseline_used: true,
      }) as Promise<PromptProposalPreview>,
    approvePromptProposal: (id: string, confirmDiffPreviewed: boolean) => {
      state.approved.push({ id, confirmDiffPreviewed });
      return Promise.resolve(state.proposals.find((p) => p.id === id));
    },
    batchApprovePromptProposals: (ids: string[], confirmDiffPreviewed: boolean) => {
      state.batchApproved.push({ ids, confirmDiffPreviewed });
      return Promise.resolve(ids.map((id) => ({ id, status: 200, body: "{}" })));
    },
    rejectPromptProposal: vi.fn(),
    restorePromptProposal: vi.fn(),
    reworkPromptProposal: vi.fn(),
    enactPromptProposal: vi.fn(),
    getRetrospectiveConfig: vi.fn(),
    listRetrospectiveRuns: () => Promise.resolve(state.retroRuns),
  },
}));

function proposal(overrides: Partial<PromptProposal> = {}): PromptProposal {
  return {
    id: "prop-1",
    workspace_id: "ws-1",
    carrier_scope: "workspace",
    carrier_scope_id: "carrier-1",
    target_section: "",
    change_kind: "add_clause",
    clause_name: "每日同步",
    clause_text: "- **每日同步**：各成员开工前在任务单回报当日计划与阻塞。",
    gate_answer_layer: "workspace tier",
    gate_answer_retention: "keeps daily coordination explicit",
    gate_answer_cost: "one bullet per run",
    gate_answer_conflict: "none — replaces nothing",
    gate_answer_dedup: "no existing daily-sync clause",
    evidence_anchors: [],
    status: "pending_owner",
    gate_errors: [],
    gate_warnings: [],
    rollback_reason: "",
    merged_from: [],
    source: "retrospective",
    created_by_type: "member",
    created_by_id: "u-2",
    audit_log: [],
    created_at: "2026-09-30T00:00:00Z",
    updated_at: "2026-09-30T00:00:00Z",
    ...overrides,
  };
}

afterEach(cleanup);

function mount(opts: { run?: string } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const searchParams = new URLSearchParams();
  if (opts.run) searchParams.set("run", opts.run);
  const navigation: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/self-evolution/proposals",
    searchParams,
    hash: "",
    getShareableUrl: (path) => path,
  };
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <NavigationProvider value={navigation}>
            <ProposalTab wsId="ws-1" />
          </NavigationProvider>
        </I18nProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

function retroRun(overrides: Partial<RetrospectiveRun> = {}): RetrospectiveRun {
  return {
    id: "run-1",
    status: "succeeded",
    trigger: "schedule",
    window_start: "2026-10-10T00:00:00Z",
    window_end: "2026-10-11T00:00:00Z",
    issues_scanned: 2,
    issues_analyzed: 2,
    proposals_created: 1,
    proposals_merged: 0,
    duplicates_skipped: 0,
    error: "",
    created_at: "2026-10-11T00:00:00Z",
    ...overrides,
  };
}

it("moves the lifecycle controls with the selected row's status", async () => {
  state.role = "owner";
  state.proposals = [proposal({ id: "prop-a", status: "pending_owner" })];
  const first = mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  expect(screen.getByRole("button", { name: "Preview diff" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Reject" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Rework" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Restore to draft" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  first.unmount();

  state.proposals = [
    proposal({
      id: "prop-b",
      status: "gate_failed",
      gate_errors: [{ line: 12, level: "error", message: "weak wording" }],
      gate_warnings: [{ line: 12, level: "warning", message: "clause overlaps an existing rule" }],
    }),
  ];
  const second = mount();
  await waitFor(() => expect(screen.getByTestId("legislation-gate-errors")).toBeTruthy());
  // The gate report keeps its structured shape end to end: line + message
  // render visibly for both findings lists, not "[object Object]".
  expect(screen.getByText(/12: weak wording/)).toBeTruthy();
  expect(screen.getByTestId("legislation-gate-warnings")).toBeTruthy();
  expect(screen.getByText(/12: clause overlaps an existing rule/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Rework" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Preview diff" })).toBeNull();
  second.unmount();

  // Rejected rows stay on the record and offer the owner the restore path.
  state.proposals = [proposal({ id: "prop-c", status: "rejected", rollback_reason: "covered by an existing clause" })];
  const third = mount();
  await waitFor(() =>
    expect(screen.getByText(/covered by an existing clause/)).toBeTruthy(),
  );
  expect(screen.getByRole("button", { name: "Restore to draft" })).toBeTruthy();
  third.unmount();

  // Enacted rows surface their enacted version and offer no lifecycle action.
  state.proposals = [proposal({ id: "prop-d", status: "enacted", enacted_version: 4 })];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-detail")).toBeTruthy());
  expect(screen.getByText("Enacted version: 4")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Rework" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Restore to draft" })).toBeNull();
});

it("keeps creation available to a member while hiding every lifecycle control", async () => {
  state.role = "member";
  state.proposals = [proposal({ created_by_id: "u-1" })];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  expect(screen.getByRole("button", { name: "New proposal" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Preview diff" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Reject" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Rework" })).toBeNull();
});

it("refuses to send the approval before the diff is rendered and confirmed", async () => {
  state.role = "owner";
  state.approved = [];
  state.proposals = [proposal({ status: "pending_owner" })];
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Preview diff" }));

  const dialog = await screen.findByTestId("proposal-preview-dialog");
  // The sandbox diff is shown before any confirmation is possible.
  await waitFor(() => expect(screen.getByText(/开工前回报当日计划/)).toBeTruthy());
  const confirm = screen.getByTestId("proposal-preview-confirm") as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);

  // Role queries skip the visually-hidden twin input the label also points at.
  fireEvent.click(
    screen.getByRole("checkbox", { name: "I have reviewed the full-text diff" }),
  );
  await waitFor(() => expect(confirm.disabled).toBe(false));
  fireEvent.click(confirm);
  await waitFor(() =>
    expect(state.approved).toEqual([{ id: "prop-1", confirmDiffPreviewed: true }]),
  );
  void dialog;
});

it("gates the batch approval behind per-id diffs and the same confirmation", async () => {
  state.role = "owner";
  state.batchApproved = [];
  state.proposals = [
    proposal({ id: "prop-a", status: "pending_owner" }),
    proposal({ id: "prop-b", status: "pending_owner", clause_name: "交付留证" }),
  ];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  fireEvent.click(screen.getByLabelText("select-prop-a"));
  fireEvent.click(screen.getByLabelText("select-prop-b"));
  fireEvent.click(screen.getByRole("button", { name: /Approve selected/ }));

  await waitFor(() => expect(screen.getByTestId("proposal-preview-diffs")).toBeTruthy());
  expect(screen.getAllByText("# Workspace Context").length).toBe(2);
  const confirm = screen.getByTestId("proposal-preview-confirm") as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);

  fireEvent.click(
    screen.getByRole("checkbox", { name: "I have reviewed the full-text diff" }),
  );
  fireEvent.click(confirm);
  await waitFor(() =>
    expect(state.batchApproved).toEqual([
      { ids: ["prop-a", "prop-b"], confirmDiffPreviewed: true },
    ]),
  );
});

it("leads with the loop narrative and filters the list by source", async () => {
  state.role = "member";
  state.proposals = [
    proposal({ id: "p-manual", source: "manual", clause_name: "manual clause" }),
    proposal({ id: "p-retro", source: "retrospective", clause_name: "retro clause" }),
    proposal({
      id: "p-merged",
      source: "manual",
      clause_name: "merged clause",
      merged_from: [{ issue_id: "i-1", run_id: "r-1" }],
    }),
  ];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());

  // Page header (RUYI-551): the automatic loop is the first sentence.
  expect(screen.getByTestId("legislation-intro").textContent).toContain(
    "enter the pool automatically",
  );

  // Unfiltered: every row (the merged row carries the merged marker).
  expect(screen.getByTestId("legislation-row-p-manual")).toBeTruthy();
  expect(screen.getByTestId("legislation-row-p-retro")).toBeTruthy();
  expect(screen.getByTestId("legislation-row-p-merged").textContent).toContain("Merged");

  // Source filter (manual / daily retrospective / merged): the segments
  // drive exactly the matching subset. Radix Tabs needs the full pointer
  // sequence, so these go through userEvent.
  await userEvent.click(screen.getByRole("tab", { name: "Daily retrospective" }));
  expect(screen.getByTestId("legislation-row-p-retro")).toBeTruthy();
  expect(screen.queryByTestId("legislation-row-p-manual")).toBeNull();
  expect(screen.queryByTestId("legislation-row-p-merged")).toBeNull();

  await userEvent.click(screen.getByRole("tab", { name: "Manual" }));
  expect(screen.getByTestId("legislation-row-p-manual")).toBeTruthy();
  expect(screen.queryByTestId("legislation-row-p-retro")).toBeNull();
  expect(screen.queryByTestId("legislation-row-p-merged")).toBeNull();

  await userEvent.click(screen.getByRole("tab", { name: "Merged" }));
  expect(screen.getByTestId("legislation-row-p-merged")).toBeTruthy();
  expect(screen.queryByTestId("legislation-row-p-manual")).toBeNull();
  expect(screen.queryByTestId("legislation-row-p-retro")).toBeNull();

  await userEvent.click(screen.getByRole("tab", { name: "All sources" }));
  expect(screen.getByTestId("legislation-row-p-manual")).toBeTruthy();
  expect(screen.getByTestId("legislation-row-p-retro")).toBeTruthy();
  expect(screen.getByTestId("legislation-row-p-merged")).toBeTruthy();
});

it("points an empty unfiltered pool at the retrospective config and hides the link when filtered", async () => {
  state.role = "member";
  state.proposals = [];
  const first = mount();
  await waitFor(() => expect(screen.getByTestId("legislation-empty")).toBeTruthy());
  const link = screen.getByTestId("legislation-empty-retro-link") as HTMLAnchorElement;
  expect(link.getAttribute("href")).toBe("/test-workspace/self-evolution/retrospective");
  first.unmount();

  // A filtered empty is an honest empty: filtering a manual-only pool by
  // "Daily retrospective" yields no rows, but the config guidance stays out
  // of the way — it only points at the config from the unfiltered view.
  state.proposals = [proposal({ id: "p-manual", source: "manual", clause_name: "manual clause" })];
  const second = mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  await userEvent.click(screen.getByRole("tab", { name: "Daily retrospective" }));
  await waitFor(() => expect(screen.getByTestId("legislation-empty")).toBeTruthy());
  expect(screen.queryByTestId("legislation-empty-retro-link")).toBeNull();
  second.unmount();
});

// RUYI-673: the results view scoped to one retrospective run's batch.
it("scopes the pool to the run in the URL and shows the run filter", async () => {
  state.role = "owner";
  state.proposals = [proposal({ id: "prop-r1" })];
  state.retroRuns = [retroRun(), retroRun({ id: "run-0", window_end: "2026-10-10T00:00:00Z" })];
  state.proposalCalls = [];
  mount({ run: "run-1" });
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  // The URL's run rides through to the API call.
  expect(state.proposalCalls.at(-1)).toEqual({ runId: "run-1" });
  // The filter renders and names the scoped run by its readable title.
  const filter = screen.getByTestId("legislation-run-filter");
  expect(filter).toBeTruthy();
  expect(screen.getAllByText(/Daily retrospective/).length).toBeGreaterThan(0);
});

it("queries the whole pool when no run is scoped", async () => {
  state.role = "owner";
  state.proposals = [proposal({ id: "prop-all" })];
  state.retroRuns = [retroRun()];
  state.proposalCalls = [];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  expect(state.proposalCalls.at(-1)).toEqual({});
});

it("updates the scoped run when the filter changes", async () => {
  const user = userEvent.setup();
  state.role = "owner";
  state.proposals = [proposal({ id: "prop-x" })];
  state.retroRuns = [retroRun()];
  state.proposalCalls = [];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-run-filter")).toBeTruthy());
  await user.click(screen.getByTestId("legislation-run-filter"));
  await user.click(await screen.findByRole("option", { name: /Daily retrospective/ }));
  await waitFor(() =>
    expect(state.proposalCalls.at(-1)).toEqual({ runId: "run-1" }),
  );
});

// RUYI-723 A: evidence anchors render as issue chips that link to the issue
// detail (list card and detail view); merged_from accumulates into aggregate
// anchors; a row without anchors renders no chip block, and legacy rows
// without the decision-context fields degrade to "—" instead of blanks.
const CHIP_A = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const CHIP_B = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const CHIP_C = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";

function anchorLinks(id: string): HTMLAnchorElement[] {
  // The tab renders anchors in both the list card and (by default) the
  // detail pane, so every chip instance must sit inside a correct link.
  return screen
    .queryAllByTestId(`issue-chip-${id}`)
    .map((el) => el.closest("a"))
    .filter((a): a is HTMLAnchorElement => a !== null);
}

it("renders evidence anchors as links to the issue detail", async () => {
  state.proposals = [
    proposal({
      evidence_anchors: [{ issue_id: CHIP_A, ref: "release script fix", note: "root cause" }],
      problem: "release script breaks on spaces",
      expected_effect: "script survives spaced paths",
      expected_gain: "no more manual fixes",
      value_score: 4,
    }),
  ];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  const links = anchorLinks(CHIP_A);
  expect(links.length).toBeGreaterThan(0);
  for (const link of links) {
    expect(link.getAttribute("href")).toBe(`/test-workspace/issues/${CHIP_A}`);
  }
  // The detail view renders the decision-context fields and the score.
  await userEvent.click(screen.getByTestId("legislation-row-prop-1"));
  await waitFor(() => expect(screen.getByText("release script breaks on spaces")).toBeTruthy());
  expect(screen.getByText("script survives spaced paths")).toBeTruthy();
  expect(screen.getByText("no more manual fixes")).toBeTruthy();
  expect(screen.getByText("4 / 5")).toBeTruthy();
});

it("renders aggregate anchors from merged_from", async () => {
  state.proposals = [
    proposal({
      evidence_anchors: [],
      merged_from: [
        { issue_id: CHIP_B, run_id: "run-1" },
        { issue_id: CHIP_C, run_id: "run-2" },
      ],
    }),
  ];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  for (const id of [CHIP_B, CHIP_C]) {
    const links = anchorLinks(id);
    expect(links.length).toBeGreaterThan(0);
    for (const link of links) {
      expect(link.getAttribute("href")).toBe(`/test-workspace/issues/${id}`);
    }
  }
});

it("degrades without anchors: no chip block renders", async () => {
  state.proposals = [proposal({})];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  expect(screen.queryByTestId(`issue-chip-${CHIP_A}`)).toBeNull();
  await userEvent.click(screen.getByTestId("legislation-row-prop-1"));
  await waitFor(() => expect(screen.getByText("Final clause text")).toBeTruthy());
  expect(screen.queryByText("Source issues")).toBeNull();
});

it("renders — for legacy rows without the decision-context fields", async () => {
  state.proposals = [proposal({ problem: undefined })];
  mount();
  await waitFor(() => expect(screen.getByTestId("legislation-list")).toBeTruthy());
  await userEvent.click(screen.getByTestId("legislation-row-prop-1"));
  await waitFor(() => expect(screen.getByText("Problem")).toBeTruthy());
  for (const label of ["Problem", "Expected effect", "Expected gain", "Value score"]) {
    const dt = screen.getByText(label);
    expect(dt.nextElementSibling?.textContent).toBe("—");
  }
});
