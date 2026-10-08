/**
 * RUYI-425 §4.3 mobile — voice instance settings screen (`more/runtimes/[id]`).
 * Pins the edit-surface semantics the acceptance criteria call out: fixed
 * facts stay read-only, the API-key tri-state (saved / probe-invalid /
 * failed) with its interpolated probe status, the clear action only when a
 * credential exists, the advanced-JSON client-side refusal, and the
 * metadata.disabled session-gate toggle.
 */
import { Alert } from "react-native";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import VoiceRuntimeSettingsScreen from "@/app/(app)/[workspace]/more/runtimes/[id]";
import type { RuntimeDevice } from "@multica/core/types";

const mockUpdateMutate = jest.fn();
const mockPutMutate = jest.fn();
const mockDeleteMutate = jest.fn();

jest.mock("expo-router", () => ({
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({ id: "rt-1" }),
  router: { back: jest.fn(), replace: jest.fn(), push: jest.fn() },
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

jest.mock("react-native-keyboard-controller", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    KeyboardAvoidingView: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
  };
});

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return { Text };
});

// Resolve against the REAL shared EN locale (packages/views/locales) so the
// assertions exercise the exact strings the screens ship — not a hand-rolled
// copy that can drift from the desktop-parity vocabulary.
jest.mock("@/lib/use-t", () => {
  const namespaces: Record<string, Record<string, unknown>> = {
    runtimes: jest.requireActual("@multica/views/locales/en/runtimes.json"),
    common: jest.requireActual("@multica/views/locales/en/common.json"),
  };
  const lookup = (ns: string, key: string): string | undefined => {
    let node: unknown = namespaces[ns];
    for (const part of key.split(".")) {
      if (node && typeof node === "object" && part in (node as Record<string, unknown>)) {
        node = (node as Record<string, unknown>)[part];
      } else {
        return undefined;
      }
    }
    return typeof node === "string" ? node : undefined;
  };
  return {
    useT: (ns?: string) => ({
      t: (
        key: string,
        fallbackOrOpts?: string | Record<string, unknown>,
        opts?: Record<string, unknown>,
      ) => {
        const template =
          lookup(ns ?? "common", key) ??
          (typeof fallbackOrOpts === "string" ? fallbackOrOpts : key);
        const o =
          typeof fallbackOrOpts === "object" && fallbackOrOpts !== null
            ? fallbackOrOpts
            : (opts ?? {});
        return template.replace(/\{\{(\w+)\}\}/g, (_, k: string) =>
          String(o[k] ?? ""),
        );
      },
      i18n: { language: "en" },
    }),
  };
});

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (s: unknown) => unknown) =>
    selector({
      currentWorkspaceId: "ws-1",
      currentWorkspaceSlug: "ws",
    }),
}));

const voiceRuntime = {
  id: "rt-1",
  workspace_id: "ws-1",
  daemon_id: null,
  name: "runtime-raw-name",
  runtime_mode: "local",
  provider: "gemini_live",
  launch_header: "",
  status: "online",
  last_seen_at: null,
  device_info: "",
  metadata: {
    model: "gemini-3.8-live",
    advanced: { temperature: 0.7 },
    disabled: false,
  },
  owner_id: null,
  visibility: "public",
  custom_name: "Gemini Live (personal)",
  credential_status: "configured",
  registration_source: "manual",
  protocol_family: "gemini_live",
  capabilities: { text: false, realtime_voice: true, tools: false },
  created_at: "",
  updated_at: "",
} as unknown as RuntimeDevice;

let mockRuntimes: RuntimeDevice[];

jest.mock("@tanstack/react-query", () => ({
  useQuery: ({ queryKey }: { queryKey: readonly string[] }) => {
    if (queryKey[0] === "runtimes") {
      return { data: mockRuntimes, isLoading: false };
    }
    throw new Error(`Unexpected query key: ${String(queryKey[0])}`);
  },
}));

jest.mock("@/data/queries/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
  runtimeProfileKeys: {
    all: (w: string) => ["runtime-profiles", w],
    list: (w: string) => ["runtime-profiles", w, "list"],
  },
  runtimeProfileListOptions: () => ({ queryKey: ["runtime-profiles"] }),
}));

jest.mock("@/data/mutations/runtimes", () => ({
  useUpdateRuntime: () => ({ mutate: mockUpdateMutate, isPending: false }),
  usePutRuntimeCredential: () => ({ mutate: mockPutMutate, isPending: false }),
  useDeleteRuntimeCredential: () => ({ mutate: mockDeleteMutate, isPending: false }),
  useCreateManualRuntime: () => ({ mutateAsync: jest.fn(), isPending: false }),
  useCreateRuntimeProfile: () => ({ mutate: jest.fn(), isPending: false }),
  // RUYI-566 delete flow hooks — inert stubs here; the delete semantics are
  // pinned by voice-runtime-detail.test.tsx.
  useDeleteRuntime: () => ({ mutate: jest.fn(), isPending: false }),
  useDeleteRuntimeProfile: () => ({ mutate: jest.fn(), isPending: false }),
}));

beforeEach(() => {
  jest.clearAllMocks();
  mockRuntimes = [voiceRuntime];
  // Default mutation behavior: surface success so alert paths run.
  mockUpdateMutate.mockImplementation(
    (_action: unknown, opts?: { onSuccess?: () => void }) =>
      opts?.onSuccess?.(),
  );
  mockPutMutate.mockImplementation(
    (_action: unknown, opts?: { onSuccess?: (res: unknown) => void }) =>
      opts?.onSuccess?.({
        runtime_id: "rt-1",
        credential_key: "api_key",
        credential_status: "configured",
        probe: { status: "ok" },
      }),
  );
  mockDeleteMutate.mockImplementation(
    (_action: unknown, opts?: { onSuccess?: () => void }) => opts?.onSuccess?.(),
  );
});

describe("VoiceRuntimeSettingsScreen", () => {
  // RNTL v14's render and fireEvent are async — await them so state flushes.
  it("renders the server-fixed facts read-only with the credential badge", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await render(<VoiceRuntimeSettingsScreen />);

    // Header shows the display name + configured badge.
    expect(screen.getByText("Gemini Live (personal)")).toBeOnTheScreen();
    expect(screen.getByText("Key configured")).toBeOnTheScreen();
    // Fixed §4.3 facts.
    expect(screen.getByText("gemini_live")).toBeOnTheScreen();
    expect(screen.getByText("Manual")).toBeOnTheScreen();
    expect(screen.getByText("Entire workspace (fixed)")).toBeOnTheScreen();
    // Enabled switch is on (metadata.disabled false).
    expect(screen.getByLabelText("Enabled").props.value).toBe(true);
    // Clear action is offered for an existing credential.
    expect(screen.getByLabelText("Remove")).toBeOnTheScreen();
    // Nothing saved on render.
    expect(mockUpdateMutate).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("refuses to save advanced params that are not a JSON object", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await render(<VoiceRuntimeSettingsScreen />);

    await fireEvent.press(screen.getByLabelText("Advanced params (JSON)"));
    await fireEvent.changeText(
      screen.getByPlaceholderText('{ "temperature": 0.7 }'),
      "[1,2]",
    );
    expect(screen.getByText("Advanced params must be a JSON object")).toBeOnTheScreen();

    // Three Save buttons (name / model / advanced) — press the advanced one.
    const saveButtons = screen.getAllByLabelText("Save");
    expect(saveButtons).toHaveLength(3);
    await fireEvent.press(saveButtons[2]);

    expect(alertSpy).toHaveBeenCalledWith("Advanced params must be a JSON object");
    expect(mockUpdateMutate).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("warns with the probe status when the connectivity check fails after a key update", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockPutMutate.mockImplementation(
      (_action: unknown, opts?: { onSuccess?: (res: unknown) => void }) =>
        opts?.onSuccess?.({
          runtime_id: "rt-1",
          credential_key: "api_key",
          credential_status: "invalid",
          probe: { status: "invalid", http_status: 400 },
        }),
    );
    await render(<VoiceRuntimeSettingsScreen />);

    await fireEvent.changeText(
      screen.getByPlaceholderText("Enter a new key to update"),
      "qaFAKE-bad-key",
    );
    await fireEvent.press(screen.getByLabelText("Update key"));

    expect(mockPutMutate).toHaveBeenCalledWith(
      {
        runtimeId: "rt-1",
        credentialKey: "api_key",
        value: "qaFAKE-bad-key",
      },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledWith(
        "Key saved, but the connectivity check failed (HTTP 400)",
      );
    });
    alertSpy.mockRestore();
  });

  it("clears the stored credential from the Remove action", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await render(<VoiceRuntimeSettingsScreen />);

    await fireEvent.press(screen.getByLabelText("Remove"));

    expect(mockDeleteMutate).toHaveBeenCalledWith(
      { runtimeId: "rt-1", credentialKey: "api_key" },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledWith("Key removed");
    });
    alertSpy.mockRestore();
  });

  it("persists the session gate through metadata.disabled when toggled off", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await render(<VoiceRuntimeSettingsScreen />);

    await fireEvent(screen.getByLabelText("Enabled"), "valueChange", false);

    expect(mockUpdateMutate).toHaveBeenCalledWith(
      { runtimeId: "rt-1", patch: { disabled: true } },
      expect.objectContaining({ onError: expect.any(Function) }),
    );
    // The toggle path is silent on success (desktop parity — the switch is
    // its own feedback).
    expect(alertSpy).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });
});
