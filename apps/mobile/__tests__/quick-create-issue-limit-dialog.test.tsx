/**
 * RUYI-605 delta: the quick-create submit path's `issue_limit_reached`
 * handling. The bare title-only Alert is replaced by the shared recovery
 * dialog (`components/billing/issue-limit-recovery.tsx`, the counterpart
 * of desktop's `IssueLimitUpgradeDialog`): the stay-on-screen surface
 * shows the limit title, the per-recovery-state description and the
 * Cloud-authorized billing action, and no native alert fires for this
 * code path anymore.
 *
 * Harness mirrors new-issue.test.tsx: the real QuickCreatePanel mounts,
 * the submit button reaches the press handler through the rendered
 * headerRight, and `useQuickCreateIssue` rejects with a structured 403
 * body the same way the server's trust boundary produces one.
 */
import React from "react";
import { Alert } from "react-native";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react-native";
import { ApiError } from "@/data/api";

const mockMutateAsync = jest.fn();

const mockFirstAgent = {
  id: "agent-first",
  workspace_id: "workspace-1",
  runtime_id: "runtime-1",
  runtime_bound: true,
  name: "First agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  archived_at: null,
  owner_id: "user-1",
  permission_mode: "public_to",
  invocation_targets: [
    { target_type: "workspace", target_id: "workspace-1" },
  ],
};

const mockSquad = {
  id: "squad-1",
  workspace_id: "workspace-1",
  name: "Squad",
  leader_id: mockFirstAgent.id,
  archived_at: null,
};

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: jest.fn(async () => null),
    setItem: jest.fn(async () => undefined),
    removeItem: jest.fn(async () => undefined),
  },
}));

jest.mock("expo-router", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  return {
    Stack: {
      Screen: ({ options }: { options?: { headerRight?: () => React.ReactNode } }) =>
        options?.headerRight
          ? React.createElement(React.Fragment, null, options.headerRight())
          : null,
    },
    router: { back: jest.fn(), push: jest.fn() },
  };
});

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

jest.mock("@react-navigation/native", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  return {
    useFocusEffect: (callback: () => void | (() => void)) => {
      React.useEffect(() => callback(), [callback]);
    },
  };
});

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

jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

jest.mock("@/components/ui/autosize-textarea", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { TextInput } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    AutosizeTextArea: (props: Record<string, unknown>) =>
      React.createElement(TextInput, {
        ...props,
        testID: "quick-create-prompt",
      }),
  };
});

jest.mock("@/components/issue/submit-issue-button", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    SubmitIssueButton: ({
      disabled,
      onPress,
    }: {
      disabled: boolean;
      onPress: () => void;
    }) =>
      React.createElement(Pressable, {
        disabled,
        onPress,
        testID: "quick-create-submit",
      }),
  };
});

jest.mock("@/components/issue/quick-create-attribute-row", () => ({
  QuickCreateAttributeRow: () => null,
}));

jest.mock("@/components/issue/mention-suggestion-bar", () => ({
  MentionSuggestionBar: () => null,
}));

jest.mock("@/data/queries/members", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
}));

jest.mock("@/data/queries/agents", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
}));

jest.mock("@/data/queries/squads", () => ({
  squadListOptions: () => ({ queryKey: ["squads"] }),
}));

jest.mock("@/data/queries/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
}));

const mockSummary = {
  availableActions: { checkout: true, portal: false, purchaseSeats: false },
};

jest.mock("@tanstack/react-query", () => ({
  queryOptions: (opts: Record<string, unknown>) => opts,
  useQuery: ({ queryKey }: { queryKey: string[] }) => {
    switch (queryKey[0]) {
      case "members":
        return { data: [{ user_id: "user-1", role: "member" }] };
      case "agents":
        return { data: [mockFirstAgent], isSuccess: true };
      case "squads":
        return { data: [mockSquad], isSuccess: true };
      case "runtimes":
        return { data: [] };
      case "config":
        return {
          data: {
            feature_flags: { billing_workspace_subscriptions: true },
          },
          isFetching: false,
        };
      case "workspace-subscriptions":
        return { data: mockSummary, isFetching: false, error: null };
      default:
        throw new Error(`Unexpected query key: ${queryKey[0]}`);
    }
  },
}));

jest.mock("@/data/mutations/issues", () => ({
  useQuickCreateIssue: () => ({
    isPending: false,
    mutateAsync: mockMutateAsync,
  }),
}));

jest.mock("@/data/use-actor-name", () => ({
  useActorLookup: () => ({
    getName: (type: string, id: string) => `${type}:${id}`,
  }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key: string, fallback: string) => fallback,
  }),
}));

jest.mock("@multica/core/runtimes/cli-version", () => ({
  checkQuickCreateCliVersion: () => ({
    state: "ok",
    current: "0.4.43",
    min: "0.4.43",
  }),
  checkQuickCreateFieldsCliVersion: () => ({
    state: "ok",
    current: "0.4.43",
    min: "0.4.43",
  }),
  readRuntimeCliVersion: () => "0.4.43",
}));

jest.mock("@/data/server-store", () => {
  const { create } = jest.requireActual<typeof import("zustand")>("zustand");
  return {
    useServerStore: create(() => ({ activeServerId: "server-1" })),
  };
});

jest.mock("@/data/workspace-store", () => {
  const { create } = jest.requireActual<typeof import("zustand")>("zustand");
  return {
    useWorkspaceStore: create(() => ({
      currentWorkspaceId: "workspace-1",
      currentWorkspaceSlug: "workspace-a",
    })),
  };
});

jest.mock("@/data/auth-store", () => {
  const { create } = jest.requireActual<typeof import("zustand")>("zustand");
  return {
    useAuthStore: create(() => ({ user: { id: "user-1" } })),
  };
});

jest.mock("@/data/stores/quick-create-prefs-store", () => {
  const actual = jest.requireActual<
    typeof import("@/data/stores/quick-create-prefs-store")
  >("@/data/stores/quick-create-prefs-store");
  return {
    ...actual,
    ensureQuickCreateActorMemoryHydrated: jest.fn(),
  };
});

// RN's Modal cannot render open under the jest harness (host stub crashes
// RNTL), so the call-site tests stub the dialog and pin the wiring; the
// dialog's copy/action semantics are covered in
// issue-limit-recovery-dialog.test.tsx against the real card.
jest.mock("@/components/billing/issue-limit-recovery", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    IssueLimitRecoveryDialog: ({
      visible,
      onClose,
    }: {
      visible: boolean;
      onClose: () => void;
    }) =>
      visible ? (
        <Pressable onPress={onClose} testID="issue-limit-dialog" />
      ) : null,
  };
});

const { QuickCreatePanel } =
  jest.requireActual<typeof import("../components/issue/quick-create-panel")>(
    "../components/issue/quick-create-panel",
  );
const { router } = jest.requireMock<typeof import("expo-router")>("expo-router");
const { useNewIssueDraftStore } = jest.requireActual<
  typeof import("@/data/stores/new-issue-draft-store")
>("@/data/stores/new-issue-draft-store");
const {
  useQuickCreateActorMemoryHydrationStore,
  useQuickCreateActorMemoryStore,
  useQuickCreatePrefsStore,
} = jest.requireActual<
  typeof import("@/data/stores/quick-create-prefs-store")
>("@/data/stores/quick-create-prefs-store");

const mockRouterBack = router.back as jest.Mock;

beforeEach(() => {
  jest.clearAllMocks();
  useNewIssueDraftStore.getState().reset();
  useQuickCreatePrefsStore.setState({ lastMode: "smart" });
  useQuickCreateActorMemoryStore.setState({ byServer: {} });
  useQuickCreateActorMemoryHydrationStore.setState({ status: "ready" });
  mockMutateAsync.mockReset();
  mockRouterBack.mockReset();
  process.env.EXPO_PUBLIC_WEB_URL = "https://web.example";
});

afterEach(async () => {
  delete process.env.EXPO_PUBLIC_WEB_URL;
  await cleanup();
});

describe("QuickCreatePanel — issue_limit_reached recovery dialog", () => {
  it("opens the shared recovery dialog instead of a bare alert and stays on screen", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockMutateAsync.mockRejectedValue(
      new ApiError("limit", 403, { code: "issue_limit_reached" }),
    );
    await render(<QuickCreatePanel />);

    // Submit gates on a seeded actor; wait for the seed chain to land
    // (same readiness signal new-issue.test.tsx uses).
    await waitFor(() => {
      expect(screen.getByText("agent:agent-first")).toBeTruthy();
    });
    await fireEvent.changeText(
      screen.getByTestId("quick-create-prompt"),
      "Create this",
    );
    // The headerRight closure only refreshes on re-render — wait for the
    // prompt state to land before pressing (same beat new-issue.test.tsx
    // waits for in submitAndClose).
    await waitFor(() => {
      expect(screen.getByTestId("quick-create-prompt").props.value).toBe(
        "Create this",
      );
    });
    fireEvent.press(screen.getByTestId("quick-create-submit"));
    await waitFor(() => {
      expect(mockMutateAsync).toHaveBeenCalledTimes(1);
    });

    expect(await screen.findByTestId("issue-limit-dialog")).toBeTruthy();
    expect(alertSpy).not.toHaveBeenCalled();
    expect(mockRouterBack).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("stays usable after the dialog is dismissed", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockMutateAsync.mockRejectedValue(
      new ApiError("limit", 403, { code: "issue_limit_reached" }),
    );
    await render(<QuickCreatePanel />);

    // Submit gates on a seeded actor; wait for the seed chain to land
    // (same readiness signal new-issue.test.tsx uses).
    await waitFor(() => {
      expect(screen.getByText("agent:agent-first")).toBeTruthy();
    });
    await fireEvent.changeText(
      screen.getByTestId("quick-create-prompt"),
      "Create this",
    );
    // The headerRight closure only refreshes on re-render — wait for the
    // prompt state to land before pressing (same beat new-issue.test.tsx
    // waits for in submitAndClose).
    await waitFor(() => {
      expect(screen.getByTestId("quick-create-prompt").props.value).toBe(
        "Create this",
      );
    });
    fireEvent.press(screen.getByTestId("quick-create-submit"));
    fireEvent.press(await screen.findByTestId("issue-limit-dialog"));

    expect(await screen.findByTestId("quick-create-submit")).toBeTruthy();
    expect(screen.queryByTestId("issue-limit-dialog")).toBeNull();
    alertSpy.mockRestore();
  });
});
