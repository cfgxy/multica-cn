import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { VoiceProvider } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

const mocks = vi.hoisted(() => ({
  useQuery: vi.fn(),
  invalidateQueries: vi.fn(),
  updateWorkspace: vi.fn(),
  toastError: vi.fn(),
  userId: "user-1",
  role: "owner" as string,
  workspaceSettings: {} as Record<string, unknown> | null,
  providers: [] as VoiceProvider[],
  createProvider: vi.fn(),
  updateProvider: vi.fn(),
  deleteProvider: vi.fn(),
  putCredential: vi.fn(),
  deleteCredential: vi.fn(),
  probeCredential: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  // queryOptions is identity here so the real options factories under test
  // can still build their descriptors.
  queryOptions: (options: unknown) => options,
  useQuery: (options: unknown) => mocks.useQuery(options),
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));

vi.mock("@multica/core/api", () => ({
  api: { updateWorkspace: mocks.updateWorkspace },
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } | null }) => unknown) =>
    selector({ user: { id: mocks.userId } }),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({
    id: "ws-1",
    slug: "acme",
    name: "Acme",
    settings: mocks.workspaceSettings,
  }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: (wsId: string) => ({ queryKey: ["members", wsId] }),
  workspaceKeys: {
    all: (wsId: string) => ["workspaces", wsId],
    list: () => ["workspaces", "list"],
  },
}));

vi.mock("@multica/core/voice/mutations", () => ({
  useCreateVoiceProvider: () => ({ mutateAsync: mocks.createProvider, isPending: false }),
  useUpdateVoiceProvider: () => ({ mutateAsync: mocks.updateProvider, isPending: false }),
  useDeleteVoiceProvider: () => ({ mutateAsync: mocks.deleteProvider, isPending: false }),
  usePutVoiceProviderCredential: () => ({ mutateAsync: mocks.putCredential, isPending: false }),
  useDeleteVoiceProviderCredential: () => ({
    mutateAsync: mocks.deleteCredential,
    isPending: false,
  }),
  useProbeVoiceProviderCredential: () => ({
    mutateAsync: mocks.probeCredential,
    isPending: false,
  }),
}));

vi.mock("sonner", () => ({
  toast: { error: mocks.toastError },
}));

import { VoiceTab } from "./voice-tab";

function providerFixture(overrides: Partial<VoiceProvider> & { id: string }): VoiceProvider {
  return {
    workspace_id: "ws-1",
    display_name: `Provider ${overrides.id}`,
    provider_type: "gemini",
    enabled: true,
    credential_status: "configured",
    credential_probe: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function primeQueries() {
  mocks.useQuery.mockImplementation((options: { queryKey: readonly unknown[] }) => {
    if (options.queryKey[0] === "members") {
      return { data: [{ user_id: mocks.userId, role: mocks.role }] };
    }
    return { data: mocks.providers, isLoading: false };
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.userId = "user-1";
  mocks.role = "owner";
  mocks.workspaceSettings = {};
  mocks.providers = [];
  mocks.updateWorkspace.mockResolvedValue({});
  primeQueries();
});

describe("VoiceTab", () => {
  it("renders every pooled provider with its type, credential and probe badges", () => {
    mocks.providers = [
      providerFixture({
        id: "p-ok",
        display_name: "Gemini OK",
        credential_probe: { status: "ok", checked_at: "2026-10-14T00:00:00Z" },
      }),
      providerFixture({ id: "p-missing", display_name: "Gemini Missing", credential_status: "missing" }),
      providerFixture({
        id: "p-chatgpt",
        display_name: "ChatGPT Pool",
        provider_type: "chatgpt",
      }),
      providerFixture({
        id: "p-unreachable",
        display_name: "Gemini Unreachable",
        credential_probe: { status: "unreachable", http_status: 0, checked_at: "2026-10-14T00:00:00Z" },
      }),
      providerFixture({
        id: "p-rejected",
        display_name: "Gemini Rejected",
        credential_probe: { status: "invalid", http_status: 401, checked_at: "2026-10-14T00:00:00Z" },
      }),
    ];

    renderWithI18n(<VoiceTab />);

    expect(screen.getByText("Gemini OK")).toBeInTheDocument();
    expect(screen.getByText("Gemini Missing")).toBeInTheDocument();
    expect(screen.getByText("ChatGPT Pool")).toBeInTheDocument();
    // Credential status badges (4 of the 5 fixtures are configured).
    expect(screen.getAllByText("Key configured")).toHaveLength(4);
    expect(screen.getByText("Key missing")).toBeInTheDocument();
    // Probe verdict badges.
    expect(screen.getByText("Connectivity OK")).toBeInTheDocument();
    expect(screen.getByText("Unreachable")).toBeInTheDocument();
    expect(screen.getByText("Key rejected")).toBeInTheDocument();
    expect(screen.getAllByText(/Checked/)).toHaveLength(3);
    // Type badges.
    expect(screen.getByText("ChatGPT")).toBeInTheDocument();
    // Admin controls render for owners.
    const switches = screen.getAllByRole("switch");
    expect(switches).toHaveLength(5);
    for (const item of switches) expect(item).toBeEnabled();
  });

  it("opens the credential dialog from the row actions menu", async () => {
    const user = userEvent.setup();
    mocks.providers = [providerFixture({ id: "p-ok", display_name: "Gemini OK" })];
    renderWithI18n(<VoiceTab />);

    await user.click(screen.getByRole("button", { name: "Gemini OK" }));
    await user.click(await screen.findByRole("menuitem", { name: /Set API key/ }));

    expect(await screen.findByText("API key — Gemini OK")).toBeInTheDocument();
  });

  it("opens the delete confirmation from the row actions menu", async () => {
    const user = userEvent.setup();
    mocks.providers = [providerFixture({ id: "p-ok", display_name: "Gemini OK" })];
    renderWithI18n(<VoiceTab />);

    await user.click(screen.getByRole("button", { name: "Gemini OK" }));
    await user.click(await screen.findByRole("menuitem", { name: /Delete/ }));

    expect(await screen.findByText("Delete voice provider")).toBeInTheDocument();
  });

  it("saves the workspace default by merging the whole settings bag, preserving ASR and sibling keys", async () => {
    const user = userEvent.setup();
    mocks.workspaceSettings = {
      issue_prefix: "RUYI",
      voice: {
        default: { provider_id: "p-ok", model: "m-1" },
        asr: { provider_id: "p-ok", model: "asr-1" },
      },
    };
    mocks.providers = [
      providerFixture({ id: "p-ok", display_name: "Gemini OK" }),
      providerFixture({ id: "p-off", display_name: "Gemini Disabled", enabled: false }),
    ];

    renderWithI18n(<VoiceTab />);

    // Editing only the default level's model must not touch the ASR level —
    // the workspace settings PATCH replaces the stored bag wholesale, so the
    // client is the one that has to merge.
    const modelInput = screen.getByDisplayValue("m-1");
    await user.clear(modelInput);
    await user.type(modelInput, "m-2");

    const saveButtons = screen.getAllByRole("button", { name: "Save" });
    await user.click(saveButtons[0]!);

    await waitFor(() => {
      expect(mocks.updateWorkspace).toHaveBeenCalledWith("ws-1", {
        settings: {
          issue_prefix: "RUYI",
          voice: {
            default: { provider_id: "p-ok", model: "m-2" },
            asr: { provider_id: "p-ok", model: "asr-1" },
          },
        },
      });
    });
    expect(mocks.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["workspaces", "list"],
    });
  });

  it("clears a chain level by saving null without disturbing the other level", async () => {
    const user = userEvent.setup();
    mocks.workspaceSettings = {
      voice: {
        default: { provider_id: "p-ok", model: "m-1" },
        asr: { provider_id: "p-ok" },
      },
    };
    mocks.providers = [providerFixture({ id: "p-ok", display_name: "Gemini OK" })];

    renderWithI18n(<VoiceTab />);

    // Deselect the default level's provider ("Not configured") through the
    // Radix Select's keyboard path, then save.
    const defaultPicker = screen.getAllByRole("combobox", { name: "Provider" })[0]!;
    defaultPicker.focus();
    await user.keyboard("{Enter}");
    const option = await screen.findByRole("option", { name: "Not configured" });
    await user.click(option);

    const saveButtons = screen.getAllByRole("button", { name: "Save" });
    await user.click(saveButtons[0]!);

    await waitFor(() => {
      expect(mocks.updateWorkspace).toHaveBeenCalledWith("ws-1", {
        settings: {
          voice: {
            default: null,
            asr: { provider_id: "p-ok" },
          },
        },
      });
    });
  });

  it("blocks saving and shows an error when advanced parameters are not a JSON object", async () => {
    const user = userEvent.setup();
    mocks.workspaceSettings = {
      voice: { default: { provider_id: "p-ok", model: "m-1" } },
    };
    mocks.providers = [providerFixture({ id: "p-ok", display_name: "Gemini OK" })];

    renderWithI18n(<VoiceTab />);

    const advancedInputs = screen.getAllByPlaceholderText('e.g. {"temperature": 0.5}');
    // user-event parses "[" / "]" as key descriptors — escape them to type
    // raw JSON text.
    await user.type(advancedInputs[0]!, "{[}1,2{]}");

    // Validation happens on save: the click surfaces the error, the parse
    // refuses to build a ref, and no settings write goes out.
    const saveButtons = screen.getAllByRole("button", { name: "Save" });
    await user.click(saveButtons[0]!);

    expect(
      screen.getByText("Advanced parameters must be a valid JSON object"),
    ).toBeInTheDocument();
    expect(mocks.updateWorkspace).not.toHaveBeenCalled();
  });

  it("renders read-only for members: hint visible, admin controls absent or disabled", () => {
    mocks.role = "member";
    mocks.providers = [
      providerFixture({ id: "p-ok", display_name: "Gemini OK", credential_status: "configured" }),
    ];

    renderWithI18n(<VoiceTab />);

    expect(
      screen.getByText("Viewing only — workspace owners and admins manage voice settings."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add provider" })).not.toBeInTheDocument();
    // Base UI Switch signals disabled via data-disabled/aria-disabled, not
    // the HTML disabled attribute.
    expect(screen.getByRole("switch")).toHaveAttribute("aria-disabled", "true");
  });
});
