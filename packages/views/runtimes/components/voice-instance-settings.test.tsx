// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enRuntimes from "../../locales/en/runtimes.json";
import {
  VoiceInstanceSettingsCard,
  isVoiceProtocolRuntime,
  parseAdvancedParams,
  readVoiceInstanceSettings,
} from "./voice-instance-settings";

const TEST_RESOURCES = { en: { runtimes: enRuntimes } };

const mockPutCredential = vi.hoisted(() => vi.fn());
const mockDeleteCredential = vi.hoisted(() => vi.fn());
const mockUpdateRuntime = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    updateRuntime: (...args: unknown[]) => mockUpdateRuntime(...args),
    putRuntimeCredential: (...args: unknown[]) => mockPutCredential(...args),
    deleteRuntimeCredential: (...args: unknown[]) =>
      mockDeleteCredential(...args),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

function makeVoiceRuntime(
  overrides: Partial<AgentRuntime> = {},
): AgentRuntime {
  return {
    id: "rt-voice-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "gemini-live-1",
    runtime_mode: "local",
    provider: "gemini_live",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-me",
    visibility: "public",
    last_seen_at: null,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    registration_source: "manual",
    protocol_family: "gemini_live",
    capabilities: { text: false, realtime_voice: true, tools: true },
    credential_status: "not_configured",
    ...overrides,
  };
}

function renderCard(
  runtime: AgentRuntime,
  canEdit = true,
): ReturnType<typeof render> {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <VoiceInstanceSettingsCard runtime={runtime} canEdit={canEdit} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

// The credential value must never appear in the DOM after a successful save:
// the password input is the only place the plaintext ever renders, and the
// update handler clears it in the same tick the mutation resolves.
describe("VoiceInstanceSettingsCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockPutCredential.mockResolvedValue({
      runtime_id: "rt-voice-1",
      credential_key: "api_key",
      credential_status: "configured",
      probe: { status: "ok" },
    });
    mockUpdateRuntime.mockResolvedValue({});
    mockDeleteCredential.mockResolvedValue(undefined);
  });

  it("types the instance and gates the form on the voice capability", () => {
    expect(isVoiceProtocolRuntime(makeVoiceRuntime())).toBe(true);
    expect(
      isVoiceProtocolRuntime(makeVoiceRuntime({ capabilities: undefined })),
    ).toBe(false);
    expect(
      isVoiceProtocolRuntime(
        makeVoiceRuntime({
          capabilities: { text: true, realtime_voice: false, tools: true },
        }),
      ),
    ).toBe(false);
  });

  it("reads §4.3 settings out of the metadata bag", () => {
    expect(readVoiceInstanceSettings({})).toEqual({
      model: "",
      advanced: null,
      disabled: false,
    });
    expect(
      readVoiceInstanceSettings({
        model: "gemini-3.8-live",
        advanced: { temperature: 0.7 },
        disabled: true,
        unrelated: "keep",
      }),
    ).toEqual({
      model: "gemini-3.8-live",
      advanced: { temperature: 0.7 },
      disabled: true,
    });
  });

  it("parseAdvancedParams accepts an object, clears on empty, fails closed otherwise", () => {
    expect(parseAdvancedParams("")).toEqual({ ok: true, value: {} });
    expect(parseAdvancedParams('{"a":1}')).toEqual({ ok: true, value: { a: 1 } });
    expect(parseAdvancedParams("{nope")).toEqual({
      ok: false,
      reason: "invalid_json",
    });
    expect(parseAdvancedParams("[1,2]")).toEqual({
      ok: false,
      reason: "not_object",
    });
    expect(parseAdvancedParams("42")).toEqual({
      ok: false,
      reason: "not_object",
    });
  });

  it("sends the key through the credential PUT and clears the input", async () => {
    renderCard(makeVoiceRuntime({ credential_status: "not_configured" }));
    const keyInput = screen.getByLabelText("API Key") as HTMLInputElement;
    fireEvent.change(keyInput, { target: { value: "sk-gemini-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Update key" }));

    await waitFor(() =>
      expect(mockPutCredential).toHaveBeenCalledWith(
        "rt-voice-1",
        "api_key",
        "sk-gemini-secret",
      ),
    );
    await waitFor(() => expect(keyInput.value).toBe(""));
  });

  it("surfaces a failed probe as a warning without blocking the save", async () => {
    const { toast } = await import("sonner");
    mockPutCredential.mockResolvedValue({
      runtime_id: "rt-voice-1",
      credential_key: "api_key",
      credential_status: "configured",
      probe: { status: "invalid", http_status: 401 },
    });
    renderCard(makeVoiceRuntime());
    fireEvent.change(screen.getByLabelText("API Key"), {
      target: { value: "sk-gemini-secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Update key" }));

    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith(
        "Key saved, but the connectivity check failed (HTTP 401)",
      ),
    );
  });

  it("surfaces an unreachable probe as could-not-verify, never invalid (RUYI-619)", async () => {
    const { toast } = await import("sonner");
    mockPutCredential.mockResolvedValue({
      runtime_id: "rt-voice-1",
      credential_key: "api_key",
      credential_status: "unreachable",
      probe: { status: "unreachable" },
    });
    renderCard(makeVoiceRuntime());
    fireEvent.change(screen.getByLabelText("API Key"), {
      target: { value: "sk-gemini-secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Update key" }));

    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith(
        "Key saved, but connectivity could not be verified (target unreachable)",
      ),
    );
    expect(toast.warning).not.toHaveBeenCalledWith(
      expect.stringContaining("connectivity check failed"),
    );
  });

  it("renders the unreachable badge with its own warning wording (RUYI-619)", () => {
    renderCard(makeVoiceRuntime({ credential_status: "unreachable" }));
    expect(screen.getByText("Can't verify")).toBeTruthy();
  });

  it("shows a server gate error instead of swallowing it (§4.3)", async () => {
    const { toast } = await import("sonner");
    mockPutCredential.mockRejectedValue(
      new Error("runtime credential storage is not configured on this server"),
    );
    renderCard(makeVoiceRuntime());
    fireEvent.change(screen.getByLabelText("API Key"), {
      target: { value: "sk-gemini-secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Update key" }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "runtime credential storage is not configured on this server",
      ),
    );
  });

  it("toggles enabled state through the disabled metadata flag", async () => {
    renderCard(makeVoiceRuntime());
    const toggle = screen.getByRole("switch", { name: "Enabled" });
    expect(toggle).not.toBeNull();
    fireEvent.click(toggle);
    await waitFor(() =>
      expect(mockUpdateRuntime).toHaveBeenCalledWith("rt-voice-1", {
        disabled: true,
      }),
    );
  });

  it("renders read-only facts when the viewer cannot edit", () => {
    renderCard(
      makeVoiceRuntime({
        metadata: { model: "gemini-3.8-live", disabled: true },
      }),
      false,
    );
    // No key control, no switch, no save buttons in read-only mode.
    expect(screen.queryByLabelText("API Key")).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    // Read-only facts still render: type, registration, fixed visibility.
    expect(screen.getByText("gemini_live")).toBeTruthy();
    expect(screen.getByText("Manual")).toBeTruthy();
    expect(screen.getByText("Entire workspace (fixed)")).toBeTruthy();
    // Disabled flag readable in read-only mode.
    expect(screen.getByText("gemini-3.8-live")).toBeTruthy();
  });

  // RUYI-564 — the name input must echo the instance's display name
  // (custom_name first, else name — runtimeDisplayName parity with the
  // mobile RUYI-540 fix). Create-only instances carry no custom_name;
  // seeding only from it left the field blank.
  describe("name field echo", () => {
    it("seeds the input with the instance name when no custom_name is set", () => {
      renderCard(makeVoiceRuntime({ custom_name: null }));
      const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
      expect(nameInput.value).toBe("gemini-live-1");
    });

    it("seeds the input with the custom name when one is set", () => {
      renderCard(makeVoiceRuntime({ custom_name: "My Voice" }));
      const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
      expect(nameInput.value).toBe("My Voice");
    });

    it("re-seeds from the display name when the instance data changes", () => {
      const { rerender } = renderCard(makeVoiceRuntime({ custom_name: null }));
      const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
      expect(nameInput.value).toBe("gemini-live-1");

      // Background refetch / re-entry delivers the renamed instance.
      rerender(
        <QueryClientProvider client={new QueryClient()}>
          <I18nProvider locale="en" resources={TEST_RESOURCES}>
            <VoiceInstanceSettingsCard
              runtime={makeVoiceRuntime({ custom_name: "Renamed Voice" })}
              canEdit
            />
          </I18nProvider>
        </QueryClientProvider>,
      );
      expect(nameInput.value).toBe("Renamed Voice");
    });

    it("saves the edit and echoes the new name on re-enter", async () => {
      const first = renderCard(makeVoiceRuntime({ custom_name: null }));
      const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
      expect(nameInput.value).toBe("gemini-live-1");

      fireEvent.change(nameInput, { target: { value: "Renamed Voice" } });
      // The card renders several "Save" buttons (name / model / advanced);
      // scope to the one in the name row.
      fireEvent.click(
        within(nameInput.closest("div") as HTMLElement).getByRole("button", {
          name: "Save",
        }),
      );
      await waitFor(() =>
        expect(mockUpdateRuntime).toHaveBeenCalledWith("rt-voice-1", {
          custom_name: "Renamed Voice",
        }),
      );

      // Fresh mount against the refetched instance (exit + re-enter).
      first.unmount();
      renderCard(makeVoiceRuntime({ custom_name: "Renamed Voice" }));
      expect(
        (screen.getByLabelText("Name") as HTMLInputElement).value,
      ).toBe("Renamed Voice");
    });
  });

  // RUYI-564 候选 2 — the input seeds from the display name, so "still
  // equals the seed" must count as unchanged: saving a create-only instance
  // untouched used to materialize its fallback name into custom_name.
  describe("name save semantics", () => {
    const nameRowSaveButton = (nameInput: HTMLInputElement) =>
      within(nameInput.closest("div") as HTMLElement).getByRole("button", {
        name: "Save",
      }) as HTMLButtonElement;

    it("keeps the save disabled while a create-only instance's name matches its seed", () => {
      renderCard(makeVoiceRuntime({ custom_name: null }));
      const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
      expect(nameInput.value).toBe("gemini-live-1");
      const saveButton = nameRowSaveButton(nameInput);
      expect(saveButton.disabled).toBe(true);
      fireEvent.click(saveButton);
      expect(mockUpdateRuntime).not.toHaveBeenCalled();
    });

    it("re-disables the save after an edit reverts to the seeded name", () => {
      renderCard(makeVoiceRuntime({ custom_name: null }));
      const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
      fireEvent.change(nameInput, { target: { value: "temp rename" } });
      const saveButton = nameRowSaveButton(nameInput);
      expect(saveButton.disabled).toBe(false);
      fireEvent.change(nameInput, { target: { value: "gemini-live-1" } });
      expect(saveButton.disabled).toBe(true);
      expect(mockUpdateRuntime).not.toHaveBeenCalled();
    });

    it("still saves a renamed alias over an existing one (no regression)", async () => {
      renderCard(makeVoiceRuntime({ custom_name: "My Voice" }));
      const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
      fireEvent.change(nameInput, { target: { value: "Renamed Voice" } });
      fireEvent.click(nameRowSaveButton(nameInput));
      await waitFor(() =>
        expect(mockUpdateRuntime).toHaveBeenCalledWith("rt-voice-1", {
          custom_name: "Renamed Voice",
        }),
      );
    });
  });
});
