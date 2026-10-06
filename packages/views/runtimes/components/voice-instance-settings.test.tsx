// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
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
});
