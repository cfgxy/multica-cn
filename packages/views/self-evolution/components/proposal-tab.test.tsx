// @vitest-environment jsdom

import { it, expect, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Proposal } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { ProposalTab } from "./proposal-tab";

/**
 * The proposal tab's wiring (RUYI-265 §A).
 *
 * The pool's invariants (prophecy at entry, adoption separate from
 * verification, rejected rows retrievable) are enforced by the server and
 * covered by its handler tests. What only a mount can show is asserted here:
 * that the owner-only lifecycle buttons appear exactly for the statuses they
 * are legal on, that a non-owner sees no lifecycle controls at all while
 * creation stays available, and that a verification cannot be submitted
 * without evidence.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  role: "owner" as string | null,
  proposals: [] as Proposal[],
  adoptedIds: [] as string[],
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ userId: "u-1", role: state.role, member: null, isLoading: false }),
}));

vi.mock("@multica/core/api", () => ({
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
  api: {
    listProposals: () => Promise.resolve(state.proposals),
    createProposal: vi.fn(),
    adoptProposal: (id: string) => {
      state.adoptedIds.push(id);
      return Promise.resolve({ status: "adopted" });
    },
    rejectProposal: vi.fn(),
    restoreProposal: vi.fn(),
    verifyProposal: vi.fn(),
  },
}));

function draftProposal(overrides: Partial<Proposal> = {}): Proposal {
  return {
    id: "prop-1",
    type: "project_cognition",
    status: "draft",
    title: "nightly failures cluster around cache",
    summary: "Cache the prompt snapshot read before the nightly sweep.",
    evidence: [],
    prophecy: {
      outcome_text: "nightly failures drop below one per week",
      falsify_condition: "a week with two or more nightly failures",
    },
    generation_snapshot: { captured_at: "2026-09-29T00:00:00Z" },
    audit_log: [{ action: "create", actor: "u-1", at: "2026-09-29T00:00:00Z" }],
    created_at: "2026-09-29T00:00:00Z",
    updated_at: "2026-09-29T00:00:00Z",
    ...overrides,
  };
}

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

it("keeps creation available to a member while hiding every lifecycle control", async () => {
  state.role = "admin";
  state.proposals = [draftProposal()];
  mount();
  await waitFor(() => expect(screen.getByRole("heading", { name: /nightly failures/ })).toBeTruthy());
  // B1: the entered prophecy is visible on the row's detail.
  expect(screen.getByText("nightly failures drop below one per week")).toBeTruthy();
  expect(screen.getByText(/a week with two or more nightly failures/)).toBeTruthy();
  // Writing to the pool is member-level; adopting is not.
  expect(screen.getByRole("button", { name: "New proposal" })).toBeTruthy();
  expect(screen.queryByTestId("proposal-owner-actions")).toBeNull();
  expect(screen.queryByRole("button", { name: "Adopt" })).toBeNull();
  expect(
    screen.getByText("Adopting, rejecting, restoring and verifying are workspace-owner only."),
  ).toBeTruthy();
});

it("offers adopt and reject for a draft only", async () => {
  state.role = "owner";
  state.proposals = [draftProposal()];
  mount();
  await waitFor(() => expect(screen.getByTestId("proposal-owner-actions")).toBeTruthy());
  expect(screen.getByRole("button", { name: "Adopt" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Reject" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Restore to draft" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Record verification/ })).toBeNull();
});

it("marks each row's source and the in-flight knowledge transfer", async () => {
  state.role = "owner";
  state.proposals = [
    draftProposal({ id: "prop-sys", created_by_type: "system" }),
    draftProposal({ id: "prop-mem" }),
  ];
  const { unmount } = mount();
  await waitFor(() => expect(screen.getByTestId("proposal-owner-actions")).toBeTruthy());
  expect(screen.getByText("System")).toBeTruthy();
  expect(screen.getByText("Member")).toBeTruthy();
  unmount();

  // While a knowledge transfer rides the daemon queue the lifecycle buttons
  // step aside: the row is neither adoptable again nor rejectable mid-flight.
  state.proposals = [
    draftProposal({ id: "prop-t", transfer_state: "transferring" }),
  ];
  mount();
  await waitFor(() => expect(screen.getByTestId("proposal-transferring")).toBeTruthy());
  expect(screen.getByText("Transferring")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Adopt" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Reject" })).toBeNull();
});

it("moves the lifecycle controls with the row's status", async () => {
  state.role = "owner";
  // B2: verify exists only after adoption.
  state.proposals = [
    draftProposal({ id: "prop-a", status: "adopted" }),
  ];
  const { unmount } = mount();
  await waitFor(() => expect(screen.getByRole("button", { name: /Record verification/ })).toBeTruthy());
  expect(screen.queryByRole("button", { name: "Adopt" })).toBeNull();
  unmount();

  // B3: a rejected row stays retrievable and offers the restore path.
  state.proposals = [draftProposal({ id: "prop-b", status: "rejected" })];
  mount();
  await waitFor(() => expect(screen.getByRole("button", { name: "Restore to draft" })).toBeTruthy());
  expect(screen.queryByRole("button", { name: "Adopt" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Record verification/ })).toBeNull();
});

it("adopts through the API when the owner confirms", async () => {
  state.role = "owner";
  state.proposals = [draftProposal()];
  state.adoptedIds = [];
  mount();
  const adopt = await screen.findByRole("button", { name: "Adopt" });
  fireEvent.click(adopt);
  await waitFor(() => expect(state.adoptedIds).toEqual(["prop-1"]));
});

it("renders recorded verification marks and requires evidence for a new one", async () => {
  state.role = "owner";
  state.proposals = [
    draftProposal({
      id: "prop-v",
      status: "adopted",
      verification: {
        marks: [
          {
            verdict: "partial",
            evidence: "median moved 400→300, inside the band only on 6 of 8 agents",
            note: "two agents regressed",
            at: "2026-09-29T01:00:00Z",
          },
        ],
      },
    }),
  ];
  mount();
  expect(
    await screen.findByText("median moved 400→300, inside the band only on 6 of 8 agents"),
  ).toBeTruthy();
  expect(screen.getByText("Partially established")).toBeTruthy();
  expect(screen.getByText("two agents regressed")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: /Record verification/ }));
  const form = await screen.findByTestId("proposal-verify-form");
  // The server refuses an evidence-less verdict; the form does not offer it.
  const submitButton = Array.from(form.querySelectorAll("button")).find(
    (b) => b.textContent === "Submit verification",
  ) as HTMLButtonElement;
  expect(submitButton.disabled).toBe(true);
  fireEvent.change(screen.getByLabelText("Evidence"), {
    target: { value: "post-adopt week: zero nightly failures" },
  });
  expect(submitButton.disabled).toBe(false);
});
