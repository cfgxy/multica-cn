import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enSquads from "../../locales/en/squads.json";

const TEST_RESOURCES = { en: { squads: enSquads } };

const mockListExecutionProfiles = vi.hoisted(() => vi.fn());
const mockActivateExecutionProfile = vi.hoisted(() => vi.fn());
const mockCreateExecutionProfile = vi.hoisted(() => vi.fn());
const mockListAgents = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    listExecutionProfiles: mockListExecutionProfiles,
    activateExecutionProfile: mockActivateExecutionProfile,
    createExecutionProfile: mockCreateExecutionProfile,
    listAgents: mockListAgents,
  },
}));

vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

// The management drawer has its own surface; stubbing it keeps this suite on
// the picker's own behaviour (selection, confirmation, result reporting).
vi.mock("./execution-profile-manage-sheet", () => ({
  ExecutionProfileManageSheet: ({ open }: { open: boolean }) =>
    open ? <div data-testid="manage-sheet" /> : null,
}));

import { ExecutionProfilePicker } from "./execution-profile-picker";

const PROFILE_A = {
  id: "p-a",
  workspace_id: "w-1",
  name: "Primary vendor",
  description: null,
  created_by: "u-1",
  is_active: true,
  entry_count: 2,
  last_activated_at: "2026-09-01T00:00:00Z",
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
  entries: [],
};

const PROFILE_B = {
  ...PROFILE_A,
  id: "p-b",
  name: "Backup vendor",
  is_active: false,
  last_activated_at: null,
};

function renderPicker() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <QueryClientProvider client={qc}>{children}</QueryClientProvider>
      </I18nProvider>
    );
  }
  return render(<ExecutionProfilePicker wsId="w-1" />, { wrapper: Wrapper });
}

describe("ExecutionProfilePicker", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListExecutionProfiles.mockResolvedValue({
      execution_profiles: [PROFILE_A, PROFILE_B],
      active_execution_profile_id: "p-a",
    });
    mockListAgents.mockResolvedValue([
      { id: "a-1", name: "Alpha" },
      { id: "a-2", name: "Beta" },
    ]);
  });

  afterEach(() => {
    cleanup();
  });

  it("shows the active profile on the trigger", async () => {
    renderPicker();
    expect(await screen.findByText("Primary vendor")).toBeInTheDocument();
  });

  it("confirms before activating and reports a clean run as a toast", async () => {
    mockActivateExecutionProfile.mockResolvedValue({
      profile: { ...PROFILE_B, is_active: true },
      applied: 2,
      skipped: 0,
      failed: 0,
      results: [
        { agent_id: "a-1", status: "applied" },
        { agent_id: "a-2", status: "applied" },
      ],
    });

    const user = userEvent.setup();
    renderPicker();
    await user.click(await screen.findByRole("button", { name: /Primary vendor/ }));
    await user.click(await screen.findByText("Backup vendor"));

    // Selecting must NOT write on its own — the confirmation is the gate.
    expect(mockActivateExecutionProfile).not.toHaveBeenCalled();
    expect(
      await screen.findByText(/Activate .Backup vendor./),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Activate" }));
    await waitFor(() =>
      expect(mockActivateExecutionProfile).toHaveBeenCalledWith("w-1", "p-b"),
    );
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalled());
    // A clean run has nothing to enumerate, so no report dialog opens.
    expect(screen.queryByText(/were not updated/)).toBeNull();
  });

  it("names the members that did not move on a partial activation", async () => {
    mockActivateExecutionProfile.mockResolvedValue({
      profile: { ...PROFILE_B, is_active: true },
      applied: 1,
      skipped: 1,
      failed: 0,
      results: [
        { agent_id: "a-1", status: "applied" },
        { agent_id: "a-2", status: "skipped", reason: "agent_archived" },
      ],
    });

    const user = userEvent.setup();
    renderPicker();
    await user.click(await screen.findByRole("button", { name: /Primary vendor/ }));
    await user.click(await screen.findByText("Backup vendor"));
    await user.click(await screen.findByRole("button", { name: "Activate" }));

    expect(
      await screen.findByText("Some members were not updated"),
    ).toBeInTheDocument();
    // Resolved to the agent's display name rather than leaving a raw uuid.
    expect(screen.getByText("Beta")).toBeInTheDocument();
    expect(screen.getByText("Member is archived")).toBeInTheDocument();
    // A partial run is not a success toast — the dialog is the report.
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("says nothing was written when no member applied", async () => {
    mockActivateExecutionProfile.mockResolvedValue({
      profile: { ...PROFILE_B, is_active: false },
      applied: 0,
      skipped: 0,
      failed: 2,
      results: [
        { agent_id: "a-1", status: "failed", reason: "runtime_unavailable" },
        { agent_id: "a-2", status: "failed", reason: "runtime_unavailable" },
      ],
    });

    const user = userEvent.setup();
    renderPicker();
    await user.click(await screen.findByRole("button", { name: /Primary vendor/ }));
    await user.click(await screen.findByText("Backup vendor"));
    await user.click(await screen.findByRole("button", { name: "Activate" }));

    expect(await screen.findByText("Nothing was activated")).toBeInTheDocument();
    expect(
      screen.getByText(/the active profile is unchanged/),
    ).toBeInTheDocument();
    // Retry is the way out of an all-failed run; without it the user has to
    // reopen the menu and re-pick the same profile.
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });

  it("does not re-activate the profile that is already active", async () => {
    const user = userEvent.setup();
    renderPicker();
    await user.click(await screen.findByRole("button", { name: /Primary vendor/ }));
    const items = await screen.findAllByText("Primary vendor");
    await user.click(items[items.length - 1]!);

    expect(screen.queryByText(/Activate .Primary vendor./)).toBeNull();
    expect(mockActivateExecutionProfile).not.toHaveBeenCalled();
  });

  it("surfaces a failed activation request as an error toast", async () => {
    mockActivateExecutionProfile.mockRejectedValue(new Error("boom"));

    const user = userEvent.setup();
    renderPicker();
    await user.click(await screen.findByRole("button", { name: /Primary vendor/ }));
    await user.click(await screen.findByText("Backup vendor"));
    await user.click(await screen.findByRole("button", { name: "Activate" }));

    await waitFor(() => expect(mockToastError).toHaveBeenCalled());
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});
