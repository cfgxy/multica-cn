// @vitest-environment jsdom

import { it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, fireEvent, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { PromptProposal, PromptProposalPreview } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { ProposalTab } from "./proposal-tab";

/**
 * The legislation tab's wiring (RUYI-305 E2).
 *
 * The state machine, permissions and gate semantics are enforced by the
 * server and covered by its handler tests. What only a mount can show is
 * asserted here: the owner-only lifecycle buttons appear exactly for the
 * statuses they are legal on, a non-owner sees no lifecycle controls at all
 * while creation stays available, and — the injection defense — the approve
 * button stays disabled until the full-text diff preview has rendered and
 * the reviewer ticked the confirmation.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  role: "owner" as string | null,
  proposals: [] as PromptProposal[],
  approved: [] as { id: string; confirmDiffPreviewed: boolean }[],
  batchApproved: [] as { ids: string[]; confirmDiffPreviewed: boolean }[],
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ userId: "u-1", role: state.role, member: null, isLoading: false }),
}));

vi.mock("@multica/core/api", () => ({
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
  api: {
    listPromptProposals: () => Promise.resolve(state.proposals),
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
    listRetrospectiveRuns: vi.fn(),
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

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ProposalTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
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
  // aria-label renders through the en dictionary (legislation.selectProposal).
  fireEvent.click(screen.getByLabelText("Select proposal prop-a"));
  fireEvent.click(screen.getByLabelText("Select proposal prop-b"));
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
