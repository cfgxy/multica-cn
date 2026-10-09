/**
 * RUYI-566 — voice instance settings screen (`more/runtimes/[id]`) delete
 * flow. Pins the acceptance semantics: the destructive entry with a
 * two-step confirm whose message carries the instance name, cancel leaves
 * zero side effects, the delete channel follows the server-enforced
 * routing (profile-backed → profile cascade endpoint, profile-less →
 * direct runtime delete), success navigates back to the list, and a
 * server refusal surfaces the body's `error` copy verbatim.
 */
import { Alert } from "react-native";
import { router } from "expo-router";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import VoiceRuntimeSettingsScreen from "@/app/(app)/[workspace]/more/runtimes/[id]";
import type { RuntimeDevice } from "@multica/core/types";

// jest.mock factories run before module-body consts initialize, so the
// router fns are created INSIDE the factory and pulled back out via
// jest.mocked — guaranteed to be the exact objects the screen calls.
const mockBack = jest.mocked(router.back);
const mockDeleteRuntime = jest.fn();
const mockDeleteProfile = jest.fn();

jest.mock("expo-router", () => ({
  Stack: { Screen: () => null },
  router: { back: jest.fn(), replace: jest.fn(), push: jest.fn() },
  useLocalSearchParams: jest.fn(() => ({ id: "rt-1" })),
}));

jest.mock("expo-haptics", () => ({
  notificationAsync: jest.fn().mockResolvedValue(undefined),
  NotificationFeedbackType: { Success: "success" },
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
    selector({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "ws" }),
}));

let mockRuntimes: RuntimeDevice[] | undefined;

jest.mock("@tanstack/react-query", () => ({
  useQuery: ({ queryKey }: { queryKey: readonly string[] }) => {
    if (queryKey[0] === "runtimes") return { data: mockRuntimes, isLoading: false };
    throw new Error(`Unexpected query key: ${String(queryKey[0])}`);
  },
}));

jest.mock("@/data/queries/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
  runtimeKeys: {
    all: (wsId: string | null) => ["runtimes", wsId] as const,
    list: (wsId: string | null) => ["runtimes", wsId, "list"] as const,
  },
  runtimeProfileKeys: {
    all: (wsId: string) => ["runtime-profiles", wsId] as const,
    list: (wsId: string) => ["runtime-profiles", wsId, "list"] as const,
  },
}));

jest.mock("@/data/mutations/runtimes", () => ({
  useUpdateRuntime: () => ({ mutate: jest.fn(), isPending: false }),
  usePutRuntimeCredential: () => ({ mutate: jest.fn(), isPending: false }),
  useDeleteRuntimeCredential: () => ({ mutate: jest.fn(), isPending: false }),
  useDeleteRuntime: () => ({ mutate: mockDeleteRuntime, isPending: false }),
  useDeleteRuntimeProfile: () => ({ mutate: mockDeleteProfile, isPending: false }),
}));

function voiceRuntime(overrides: Partial<RuntimeDevice> = {}): RuntimeDevice {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Gemini Live",
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
    custom_name: "Front desk mic",
    profile_id: "prof-1",
    protocol_family: "gemini_live",
    credential_status: "not_configured",
    ...overrides,
  } as unknown as RuntimeDevice;
}

beforeEach(() => {
  jest.clearAllMocks();
  mockRuntimes = [voiceRuntime()];
});

async function renderDetail() {
  const utils = await render(<VoiceRuntimeSettingsScreen />);
  return utils;
}

describe("VoiceRuntimeSettingsScreen delete flow (RUYI-566)", () => {
  it("opens a confirm dialog whose message carries the instance name", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await renderDetail();

    await fireEvent.press(screen.getByLabelText("Delete instance"));

    expect(alertSpy).toHaveBeenCalledTimes(1);
    const [title, message, buttons] = alertSpy.mock.calls[0];
    expect(title).toBe("Delete instance?");
    expect(message).toContain("Front desk mic");
    expect(buttons).toEqual([
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: expect.any(Function),
      },
    ]);
    alertSpy.mockRestore();
  });

  it("cancel leaves zero side effects — no delete call, no navigation", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await renderDetail();

    await fireEvent.press(screen.getByLabelText("Delete instance"));
    const [, , buttons] = alertSpy.mock.calls[0];
    buttons?.[0]?.onPress?.();

    expect(mockDeleteProfile).not.toHaveBeenCalled();
    expect(mockDeleteRuntime).not.toHaveBeenCalled();
    expect(mockBack).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("profile-backed instance deletes through the profile cascade channel", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await renderDetail();

    await fireEvent.press(screen.getByLabelText("Delete instance"));
    const [, , buttons] = alertSpy.mock.calls[0];
    buttons?.[1]?.onPress?.();

    expect(mockDeleteProfile).toHaveBeenCalledTimes(1);
    expect(mockDeleteProfile.mock.calls[0][0]).toBe("prof-1");
    expect(mockDeleteRuntime).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("profile-less instance deletes through the direct runtime channel", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockRuntimes = [voiceRuntime({ profile_id: null })];
    await renderDetail();

    await fireEvent.press(screen.getByLabelText("Delete instance"));
    const [, , buttons] = alertSpy.mock.calls[0];
    buttons?.[1]?.onPress?.();

    expect(mockDeleteRuntime).toHaveBeenCalledTimes(1);
    expect(mockDeleteRuntime.mock.calls[0][0]).toBe("rt-1");
    expect(mockDeleteProfile).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("navigates back to the list after a successful delete", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await renderDetail();

    await fireEvent.press(screen.getByLabelText("Delete instance"));
    const [, , buttons] = alertSpy.mock.calls[0];
    buttons?.[1]?.onPress?.();

    const opts = mockDeleteProfile.mock.calls[0][1];
    opts.onSuccess();
    await waitFor(() => expect(mockBack).toHaveBeenCalled());
    alertSpy.mockRestore();
  });

  it("surfaces the server refusal and stays on the page", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await renderDetail();

    await fireEvent.press(screen.getByLabelText("Delete instance"));
    const [, , buttons] = alertSpy.mock.calls[0];
    buttons?.[1]?.onPress?.();

    alertSpy.mockClear();
    const opts = mockDeleteProfile.mock.calls[0][1];
    opts.onError(
      Object.assign(new Error("409 Conflict"), {
        status: 409,
        body: {
          error:
            "cannot delete runtime: agents still reference it as their voice runtime. Unbind them first.",
          code: "runtime_has_voice_bindings",
        },
      }),
    );
    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledWith(
        "cannot delete runtime: agents still reference it as their voice runtime. Unbind them first.",
      );
    });
    expect(mockBack).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });
});
