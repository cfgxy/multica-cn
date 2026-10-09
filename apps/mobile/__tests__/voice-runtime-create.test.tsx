/**
 * RUYI-425 §4.3 mobile — voice instance create screen (`more/runtimes/new`).
 * Pins the desktop-parity semantics that the acceptance criteria call out:
 * the no-profile escape hatch, the voice-only Type filter, the advanced-
 * params JSON gate, and the save-triggers-probe tri-state feedback. Since
 * RUYI-626 the probe verdict is the device's (direct path dials Google from
 * the user's network), so the device probe is mocked per test.
 */
import { Alert } from "react-native";
import { router } from "expo-router";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import NewVoiceRuntimeScreen from "@/app/(app)/[workspace]/more/runtimes/new";
import { probeVoiceCredential } from "@/lib/voice/probe";
import type { RuntimeDevice, RuntimeProfile } from "@multica/core/types";

// jest.mock factories run before module-body consts initialize, so the
// router fns are created INSIDE the factory and pulled back out via
// jest.mocked — guaranteed to be the exact objects the screen calls.
const mockReplace = jest.mocked(router.replace);
const mockInvalidateQueries = jest.fn();
const mockCreateRuntimeAsync = jest.fn();
const mockPutCredentialAsync = jest.fn();
const mockCreateProfile = jest.fn();
const mockProbeVoiceCredential = jest.mocked(probeVoiceCredential);

jest.mock("@/lib/voice/probe", () => ({
  probeVoiceCredential: jest.fn(),
}));

jest.mock("expo-router", () => ({
  Stack: { Screen: () => null },
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

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

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

const voiceProfile = {
  id: "prof-voice",
  workspace_id: "ws-1",
  display_name: "Gemini Live",
  protocol_family: "gemini_live",
  command_name: "",
  description: null,
  fixed_args: [],
  visibility: "workspace",
  created_by: null,
  enabled: true,
  capabilities: { text: false, realtime_voice: true, tools: false },
  created_at: "",
  updated_at: "",
} as RuntimeProfile;

const cliProfile: RuntimeProfile = {
  ...voiceProfile,
  id: "prof-cli",
  display_name: "Codex",
  protocol_family: "codex",
  command_name: "codex",
  capabilities: { text: true, realtime_voice: false, tools: true },
};

const createdRuntime = {
  id: "rt-new",
  workspace_id: "ws-1",
  daemon_id: null,
  name: "Gemini Live (personal)",
  runtime_mode: "local",
  provider: "gemini_live",
  launch_header: "",
  status: "online",
  device_info: "",
  metadata: {},
  owner_id: null,
  visibility: "public",
  last_seen_at: null,
  created_at: "",
  updated_at: "",
  registration_source: "manual",
} as unknown as RuntimeDevice;

let mockProfiles: RuntimeProfile[];

jest.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mockInvalidateQueries }),
  useQuery: ({ queryKey }: { queryKey: readonly string[] }) => {
    switch (queryKey[0]) {
      case "runtime-profiles":
        return { data: mockProfiles };
      default:
        throw new Error(`Unexpected query key: ${String(queryKey[0])}`);
    }
  },
}));

jest.mock("@/data/queries/runtimes", () => ({
  runtimeProfileKeys: {
    all: (wsId: string) => ["runtime-profiles", wsId] as const,
    list: (wsId: string) => ["runtime-profiles", wsId, "list"] as const,
  },
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
  runtimeProfileListOptions: () => ({ queryKey: ["runtime-profiles"] }),
}));

jest.mock("@/data/mutations/runtimes", () => ({
  useCreateManualRuntime: () => ({
    mutateAsync: mockCreateRuntimeAsync,
    isPending: false,
  }),
  usePutRuntimeCredential: () => ({
    mutateAsync: mockPutCredentialAsync,
    isPending: false,
  }),
  useCreateRuntimeProfile: () => ({
    mutate: mockCreateProfile,
    isPending: false,
  }),
  useUpdateRuntime: () => ({ mutate: jest.fn(), isPending: false }),
  useDeleteRuntimeCredential: () => ({ mutate: jest.fn(), isPending: false }),
}));

beforeEach(() => {
  jest.clearAllMocks();
  mockProfiles = [cliProfile, voiceProfile];
  // Default device-probe verdict: reachable and valid (RUYI-626).
  mockProbeVoiceCredential.mockResolvedValue({ status: "ok" });
});

describe("NewVoiceRuntimeScreen", () => {
  // RNTL v14's render is async — every screen query must run after awaiting it.
  it("filters the Type list to voice-family profiles only", async () => {
    await render(<NewVoiceRuntimeScreen />);
    expect(screen.getByText("Gemini Live")).toBeOnTheScreen();
    expect(screen.queryByText("Codex")).not.toBeOnTheScreen();
  });

  it("offers the one-click default profile when the workspace has none", async () => {
    mockProfiles = [];
    await render(<NewVoiceRuntimeScreen />);
    expect(
      screen.getByText("No voice profile in this workspace yet."),
    ).toBeOnTheScreen();
    await fireEvent.press(
      screen.getByLabelText("Create Gemini Live profile"),
    );
    expect(mockCreateProfile).toHaveBeenCalledWith(
      { display_name: "Gemini Live", protocol_family: "gemini_live", command_name: "" },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
  });

  it("blocks submit while the advanced params are not a JSON object", async () => {
    await render(<NewVoiceRuntimeScreen />);
    // Expand the advanced block, then type a non-object JSON payload.
    await fireEvent.press(screen.getByLabelText("Advanced params (JSON)"));
    const advanced = screen.getByPlaceholderText('{ "temperature": 0.7 }');
    await fireEvent.changeText(advanced, "[1,2]");
    expect(screen.getByLabelText("Register")).toBeDisabled();
    expect(
      screen.getByText("Advanced params must be a JSON object"),
    ).toBeOnTheScreen();
  });

  it("registers, stores the key, and warns when the device probe rejects the key (tri-state)", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockCreateRuntimeAsync.mockResolvedValue(createdRuntime);
    mockPutCredentialAsync.mockResolvedValue({
      runtime_id: "rt-new",
      credential_key: "api_key",
      credential_status: "invalid",
      probe: { status: "invalid", http_status: 400 },
    });
    // RUYI-626: the alert follows the DEVICE probe verdict, not the PUT
    // response's server-side probe (that describes the server's egress).
    mockProbeVoiceCredential.mockResolvedValue({
      status: "invalid",
      httpStatus: 400,
    });
    await render(<NewVoiceRuntimeScreen />);

    await fireEvent.changeText(
      screen.getByPlaceholderText("e.g. Gemini Live (personal)"),
      "Gemini Live (personal)",
    );
    await fireEvent.changeText(
      screen.getByPlaceholderText("Paste the Gemini API key"),
      "qaFAKE-key",
    );
    await fireEvent.press(screen.getByLabelText("Register"));

    await waitFor(() => {
      expect(mockCreateRuntimeAsync).toHaveBeenCalledWith({
        name: "Gemini Live (personal)",
        profile_id: "prof-voice",
      });
    });
    expect(mockPutCredentialAsync).toHaveBeenCalledWith({
      runtimeId: "rt-new",
      credentialKey: "api_key",
      value: "qaFAKE-key",
    });
    await waitFor(() => {
      // No server error in this path — the optional detail line stays unset.
      expect(alertSpy).toHaveBeenCalledWith(
        "Registered, but the connectivity check failed — check the API key",
        undefined,
      );
    });
    // Success still lands on the new instance's settings page — the
    // manual/online/public presentation lives there.
    expect(mockReplace).toHaveBeenCalledWith({
      pathname: "/[workspace]/more/runtimes/[id]",
      params: { workspace: "ws", id: "rt-new" },
    });
    alertSpy.mockRestore();
  });

  it("keeps the instance when the credential PUT fails and surfaces the server message (RUYI-540)", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockCreateRuntimeAsync.mockResolvedValue(createdRuntime);
    // The fail-closed 503 a server without MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY
    // returns — the alert must carry the server's readable message, not just
    // the generic localized copy (the reported bug showed a bare "503").
    const serverMessage =
      "runtime credential encryption is not configured on this server (MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY missing)";
    mockPutCredentialAsync.mockRejectedValue(new Error(serverMessage));
    await render(<NewVoiceRuntimeScreen />);

    await fireEvent.changeText(
      screen.getByPlaceholderText("e.g. Gemini Live (personal)"),
      "Dup name allowed",
    );
    await fireEvent.changeText(
      screen.getByPlaceholderText("Paste the Gemini API key"),
      "qaFAKE-key",
    );
    await fireEvent.press(screen.getByLabelText("Register"));

    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledWith(
        "Registered, but saving the API key failed — add it from the instance settings",
        serverMessage,
      );
    });
    // A failed key save never deletes the instance — navigation proceeds.
    expect(mockReplace).toHaveBeenCalledWith(
      expect.objectContaining({ pathname: "/[workspace]/more/runtimes/[id]" }),
    );
    alertSpy.mockRestore();
  });
});
