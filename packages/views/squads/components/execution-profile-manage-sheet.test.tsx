import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enSquads from "../../locales/en/squads.json";

const TEST_RESOURCES = { en: { squads: enSquads } };

const mockListExecutionProfiles = vi.hoisted(() => vi.fn());
const mockGetExecutionProfile = vi.hoisted(() => vi.fn());
const mockUpdateExecutionProfile = vi.hoisted(() => vi.fn());
const mockDeleteExecutionProfile = vi.hoisted(() => vi.fn());
const mockUpsertEntry = vi.hoisted(() => vi.fn());
const mockDeleteEntry = vi.hoisted(() => vi.fn());
const mockListAgents = vi.hoisted(() => vi.fn());
const mockListRuntimes = vi.hoisted(() => vi.fn());
const mockListMembers = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: {
    listExecutionProfiles: mockListExecutionProfiles,
    getExecutionProfile: mockGetExecutionProfile,
    updateExecutionProfile: mockUpdateExecutionProfile,
    deleteExecutionProfile: mockDeleteExecutionProfile,
    upsertExecutionProfileEntry: mockUpsertEntry,
    deleteExecutionProfileEntry: mockDeleteEntry,
    listAgents: mockListAgents,
    listRuntimes: mockListRuntimes,
    listMembers: mockListMembers,
  },
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: unknown) => unknown) => {
      const state = { user: { id: "u-1" } };
      return selector ? selector(state) : state;
    },
    { getState: () => ({ user: { id: "u-1" } }) },
  ),
}));

// The three execution pickers each own a popover and a models query; this
// suite is about the entry lifecycle around them, so they are stubbed with
// controls that report and set the same values.
vi.mock("../../agents/components/inspector/runtime-picker", () => ({
  RuntimePicker: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (v: string) => void;
  }) => (
    <button data-testid="runtime-picker" onClick={() => onChange("r-2")}>
      runtime:{value || "none"}
    </button>
  ),
}));

vi.mock("../../agents/components/inspector/model-picker", () => ({
  ModelPicker: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (v: string) => void;
  }) => (
    <button data-testid="model-picker" onClick={() => onChange("model-x")}>
      model:{value || "none"}
    </button>
  ),
}));

vi.mock("../../agents/components/inspector/thinking-prop-row", () => ({
  ThinkingSettingField: ({ value }: { value: string }) => (
    <div data-testid="thinking-field">thinking:{value || "none"}</div>
  ),
}));

import { ExecutionProfileManageSheet } from "./execution-profile-manage-sheet";

const PROFILE = {
  id: "p-a",
  workspace_id: "w-1",
  name: "Primary vendor",
  description: null,
  created_by: "u-1",
  is_active: true,
  entry_count: 1,
  last_activated_at: null,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
  entries: [
    {
      agent_id: "a-1",
      runtime_id: "r-1",
      model: "model-a",
      thinking_level: "",
      updated_at: "2026-09-01T00:00:00Z",
    },
  ],
};

function renderSheet(initialProfileId: string | null = "p-a") {
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
  return render(
    <ExecutionProfileManageSheet
      wsId="w-1"
      open
      initialProfileId={initialProfileId}
      onOpenChange={() => {}}
    />,
    { wrapper: Wrapper },
  );
}

describe("ExecutionProfileManageSheet", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListExecutionProfiles.mockResolvedValue({
      execution_profiles: [PROFILE],
      active_execution_profile_id: "p-a",
    });
    mockGetExecutionProfile.mockResolvedValue(PROFILE);
    mockListAgents.mockResolvedValue([
      { id: "a-1", name: "Alpha", runtime_id: "r-1", model: "model-a", archived_at: null },
      { id: "a-2", name: "Beta", runtime_id: "", model: "", archived_at: null },
    ]);
    mockListRuntimes.mockResolvedValue([
      { id: "r-1", name: "Box", provider: "claude", status: "online" },
      { id: "r-2", name: "Box 2", provider: "codex", status: "online" },
    ]);
    mockListMembers.mockResolvedValue([]);
    mockUpsertEntry.mockResolvedValue(PROFILE.entries[0]);
  });

  afterEach(() => {
    cleanup();
  });

  it("lists every live agent as a configurable slot", async () => {
    renderSheet();
    expect(await screen.findByText("Alpha")).toBeInTheDocument();
    expect(screen.getByText("Beta")).toBeInTheDocument();
  });

  it("only offers Remove for members the profile already names", async () => {
    renderSheet();
    await screen.findByText("Alpha");
    // One stored entry (Alpha) → exactly one Remove control.
    expect(screen.getAllByRole("button", { name: "Remove" })).toHaveLength(1);
  });

  it("refuses to save an entry with no model chosen", async () => {
    const user = userEvent.setup();
    renderSheet();
    await screen.findByText("Beta");

    // Beta has no runtime and no model; pick only a runtime and the save must
    // stay blocked — the server rejects half-filled entries because a stored
    // entry has to be activatable.
    const runtimePickers = screen.getAllByTestId("runtime-picker");
    await user.click(runtimePickers[1]!);

    const saveButtons = screen.getAllByRole("button", { name: "Save" });
    expect(saveButtons[1]).toBeDisabled();
    expect(screen.getByText("Pick both a runtime and a model")).toBeInTheDocument();

    await user.click(screen.getAllByTestId("model-picker")[1]!);
    await waitFor(() =>
      expect(screen.getAllByRole("button", { name: "Save" })[1]).toBeEnabled(),
    );
  });

  it("writes runtime, model and thinking level in one PUT", async () => {
    const user = userEvent.setup();
    renderSheet();
    await screen.findByText("Beta");

    await user.click(screen.getAllByTestId("runtime-picker")[1]!);
    await user.click(screen.getAllByTestId("model-picker")[1]!);
    await user.click(screen.getAllByRole("button", { name: "Save" })[1]!);

    await waitFor(() =>
      expect(mockUpsertEntry).toHaveBeenCalledWith("w-1", "p-a", {
        agent_id: "a-2",
        runtime_id: "r-2",
        model: "model-x",
        thinking_level: "",
      }),
    );
  });

  it("clears the staged model when the runtime changes", async () => {
    const user = userEvent.setup();
    renderSheet();
    await screen.findByText("Alpha");

    // Alpha starts from its stored entry.
    expect(screen.getAllByTestId("model-picker")[0]).toHaveTextContent(
      "model:model-a",
    );
    await user.click(screen.getAllByTestId("runtime-picker")[0]!);
    // Model and thinking level are runtime-native; keeping them would stage a
    // combination the server rejects.
    await waitFor(() =>
      expect(screen.getAllByTestId("model-picker")[0]).toHaveTextContent(
        "model:none",
      ),
    );
    expect(screen.getAllByRole("button", { name: "Save" })[0]).toBeDisabled();
  });

  it("warns that deleting the active profile does not roll members back", async () => {
    const user = userEvent.setup();
    renderSheet();
    await screen.findByText("Alpha");

    await user.click(screen.getByRole("button", { name: /Delete profile/ }));
    expect(
      await screen.findByText(/only clears the active marker/),
    ).toBeInTheDocument();
  });

  it("treats clearing a no-opinion thinking level as a change worth saving", async () => {
    // A stored null means the entry has no opinion and activation leaves the
    // member's level alone; the drawer's empty field means "runtime default",
    // which activation writes as an explicit clear. Collapsing the two would
    // leave Save disabled and make the clear unreachable from the UI.
    const noOpinion = {
      ...PROFILE,
      entries: [{ ...PROFILE.entries[0]!, thinking_level: null }],
    };
    mockListExecutionProfiles.mockResolvedValue({
      execution_profiles: [noOpinion],
      active_execution_profile_id: "p-a",
    });
    mockGetExecutionProfile.mockResolvedValue(noOpinion);

    const user = userEvent.setup();
    renderSheet();
    await screen.findByText("Alpha");

    const save = screen.getAllByRole("button", { name: "Save" })[0]!;
    expect(save).toBeEnabled();
    await user.click(save);

    await waitFor(() =>
      expect(mockUpsertEntry).toHaveBeenCalledWith("w-1", "p-a", {
        agent_id: "a-1",
        runtime_id: "r-1",
        model: "model-a",
        thinking_level: "",
      }),
    );
  });

  it("shows a duplicate name inline instead of a silent no-op", async () => {
    const { ApiError } = await import("@multica/core/api");
    mockUpdateExecutionProfile.mockRejectedValue(
      new ApiError("conflict", 409, "Conflict", {
        error: "A profile with that name already exists",
      }),
    );

    const user = userEvent.setup();
    renderSheet();
    const input = await screen.findByLabelText("Profile name");
    await user.clear(input);
    await user.type(input, "Backup vendor");
    await user.tab();

    expect(
      await screen.findByText("A profile with that name already exists"),
    ).toBeInTheDocument();
  });
});
