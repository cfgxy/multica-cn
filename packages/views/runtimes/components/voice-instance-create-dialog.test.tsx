// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { RuntimeProfile } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enRuntimes from "../../locales/en/runtimes.json";
import enCommon from "../../locales/en/common.json";
import {
  VoiceInstanceCreateDialog,
  isVoiceProfile,
} from "./voice-instance-create-dialog";

const TEST_RESOURCES = {
  en: { runtimes: enRuntimes, common: enCommon },
};

const mockCreateRuntime = vi.hoisted(() => vi.fn());
const mockPutCredential = vi.hoisted(() => vi.fn());
const mockListProfiles = vi.hoisted(() => vi.fn());
const mockCreateProfile = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    createManualRuntime: (...args: unknown[]) => mockCreateRuntime(...args),
    putRuntimeCredential: (...args: unknown[]) => mockPutCredential(...args),
    listRuntimeProfiles: (...args: unknown[]) => mockListProfiles(...args),
    createRuntimeProfile: (...args: unknown[]) => mockCreateProfile(...args),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

function makeProfile(
  overrides: Partial<RuntimeProfile> = {},
): RuntimeProfile {
  return {
    id: "profile-voice-1",
    workspace_id: "ws-1",
    display_name: "Gemini Live",
    protocol_family: "gemini_live",
    command_name: "",
    description: null,
    fixed_args: [],
    visibility: "workspace",
    created_by: null,
    enabled: true,
    capabilities: { text: false, realtime_voice: true, tools: true },
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

function makeCreatedRuntime() {
  return {
    id: "rt-new-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "My Gemini",
    runtime_mode: "cloud",
    provider: "gemini_live",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-me",
    visibility: "public",
    last_seen_at: null,
    created_at: "2026-10-05T00:00:00Z",
    updated_at: "2026-10-05T00:00:00Z",
    registration_source: "manual",
    protocol_family: "gemini_live",
    capabilities: { text: false, realtime_voice: true, tools: true },
    credential_status: "not_configured",
  };
}

function renderDialog(props: Partial<Parameters<typeof VoiceInstanceCreateDialog>[0]> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <VoiceInstanceCreateDialog onClose={props.onClose ?? (() => {})} {...props} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("isVoiceProfile", () => {
  it("filters the create form's Type options on the realtime_voice capability", () => {
    expect(isVoiceProfile(makeProfile())).toBe(true);
    expect(isVoiceProfile(makeProfile({ capabilities: undefined }))).toBe(false);
    expect(
      isVoiceProfile(
        makeProfile({
          protocol_family: "codex",
          capabilities: { text: true, realtime_voice: false, tools: true },
        }),
      ),
    ).toBe(false);
  });
});

describe("VoiceInstanceCreateDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListProfiles.mockResolvedValue([makeProfile()]);
    mockCreateRuntime.mockResolvedValue(makeCreatedRuntime());
    mockPutCredential.mockResolvedValue({
      runtime_id: "rt-new-1",
      credential_key: "api_key",
      credential_status: "configured",
      probe: { status: "ok" },
    });
  });

  it("registers with the default-selected voice profile and triggers the probe via the credential save", async () => {
    const onClose = vi.fn();
    const onCreated = vi.fn();
    renderDialog({ onClose, onCreated });

    const nameInput = await screen.findByLabelText("Name");
    fireEvent.change(nameInput, { target: { value: "My Gemini" } });
    const keyInput = screen.getByLabelText("API Key");
    fireEvent.change(keyInput, { target: { value: "test-key-abc" } });

    fireEvent.click(screen.getByRole("button", { name: "Register" }));

    await waitFor(() => expect(mockCreateRuntime).toHaveBeenCalledTimes(1));
    expect(mockCreateRuntime).toHaveBeenCalledWith({
      name: "My Gemini",
      profile_id: "profile-voice-1",
    });
    // §4.3: creating the credential triggers the connectivity probe
    // server-side; the client just persists the key right after create.
    await waitFor(() =>
      expect(mockPutCredential).toHaveBeenCalledWith(
        "rt-new-1",
        "api_key",
        "test-key-abc",
      ),
    );
    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("passes the model and advanced params through to the registration call", async () => {
    renderDialog();

    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "Tuned" },
    });
    fireEvent.change(screen.getByLabelText("Model"), {
      target: { value: "my-model-1" },
    });
    fireEvent.change(screen.getByLabelText("Advanced params (JSON)"), {
      target: { value: '{"temperature":0.5}' },
    });
    fireEvent.click(screen.getByRole("button", { name: "Register" }));

    await waitFor(() => expect(mockCreateRuntime).toHaveBeenCalledTimes(1));
    expect(mockCreateRuntime).toHaveBeenCalledWith({
      name: "Tuned",
      profile_id: "profile-voice-1",
      model: "my-model-1",
      advanced: { temperature: 0.5 },
    });
  });

  it("disables submit on malformed advanced JSON and when the name is blank", async () => {
    renderDialog();

    const submit = await screen.findByRole("button", { name: "Register" });
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "X" },
    });
    fireEvent.change(screen.getByLabelText("Advanced params (JSON)"), {
      target: { value: "[1,2]" },
    });
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Advanced params (JSON)"), {
      target: { value: '{"ok":true}' },
    });
    expect(submit).toBeEnabled();
    expect(mockCreateRuntime).not.toHaveBeenCalled();
  });

  it("warns instead of celebrating when the probe reports the key invalid", async () => {
    const { toast } = await import("sonner");
    mockPutCredential.mockResolvedValue({
      runtime_id: "rt-new-1",
      credential_key: "api_key",
      credential_status: "invalid",
      probe: { status: "invalid", http_status: 401 },
    });
    const onClose = vi.fn();
    renderDialog({ onClose });

    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "Bad Key" },
    });
    fireEvent.change(screen.getByLabelText("API Key"), {
      target: { value: "revoked-key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Register" }));

    await waitFor(() => expect(toast.warning).toHaveBeenCalled());
    // §4.5: a failed probe never rolls the registration back.
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("warns with could-not-verify wording when the probe reports unreachable (RUYI-619)", async () => {
    const { toast } = await import("sonner");
    mockPutCredential.mockResolvedValue({
      runtime_id: "rt-new-1",
      credential_key: "api_key",
      credential_status: "unreachable",
      probe: { status: "unreachable" },
    });
    const onClose = vi.fn();
    renderDialog({ onClose });

    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "Offline Probe" },
    });
    fireEvent.change(screen.getByLabelText("API Key"), {
      target: { value: "qaFAKE-key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Register" }));

    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith(
        "Registered, but connectivity could not be verified — the target may be unreachable",
      ),
    );
    expect(toast.warning).not.toHaveBeenCalledWith(
      "Registered, but the connectivity check failed — check the API key",
    );
    // Registration still lands (§4.5).
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("offers the one-click default profile when the workspace has none", async () => {
    mockListProfiles.mockResolvedValue([]);
    mockCreateProfile.mockResolvedValue(
      makeProfile({ id: "profile-new-1" }),
    );
    renderDialog();

    const createButton = await screen.findByRole("button", {
      name: "Create Gemini Live profile",
    });
    expect(screen.queryByRole("button", { name: "Register" })).toBeDisabled();
    fireEvent.click(createButton);

    await waitFor(() => expect(mockCreateProfile).toHaveBeenCalledTimes(1));
    // Voice families are command-less by API contract.
    expect(mockCreateProfile).toHaveBeenCalledWith(
      "ws-1",
      expect.objectContaining({ protocol_family: "gemini_live", command_name: "" }),
    );
  });

  it("closes with a warning and keeps the instance recoverable when the credential save fails outright", async () => {
    const { toast } = await import("sonner");
    mockPutCredential.mockRejectedValue(new Error("503"));
    const onClose = vi.fn();
    renderDialog({ onClose });

    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "Retry Later" },
    });
    fireEvent.change(screen.getByLabelText("API Key"), {
      target: { value: "k" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Register" }));

    await waitFor(() => expect(toast.warning).toHaveBeenCalled());
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mockCreateRuntime).toHaveBeenCalledTimes(1);
  });
});
