// @ts-nocheck
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import type { QuickCreateActorRef } from "@/lib/quick-create";

/**
 * RUYI-624 — the agent detail page's "+ Assign work" seeds the quick-create
 * smart actor with the agent BEFORE pushing new-issue (desktop opens the
 * quick-create modal with initialMode="agent" + a seeded actor). Mobile's
 * new-issue mount reset used to wipe that seed, so the panel fell back to
 * last-actor memory / the first visible agent — the reported defect.
 * Contract pinned here:
 *
 *   - a pre-navigation smartActor seed survives the mount reset and wins
 *     over memory / first-visible-agent fallbacks;
 *   - while a seed is in effect, Smart mode is forced for the visit even
 *     when the remembered mode is Manual — WITHOUT writing the remembered
 *     preference;
 *   - the submitted body targets the seeded agent (agent_id);
 *   - an explicit tab touch hands mode control back to the user (remembered
 *     mode takes over), and the actor itself survives mode flips;
 *   - with no seed, the remembered mode is respected and no actor is
 *     fabricated by the mount reset itself.
 */

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

const mockSecondAgent = {
  ...mockFirstAgent,
  id: "agent-second",
  name: "Second agent",
};

const mockSquad = {
  id: "squad-1",
  workspace_id: "workspace-1",
  name: "Squad",
  leader_id: mockFirstAgent.id,
  archived_at: null,
};

const seededActor: QuickCreateActorRef = { type: "agent", id: mockSecondAgent.id };

// RUYI-477: quick-create-panel mounts an ActionSheetModal that needs the
// safe-area + theme context (same pattern as new-issue.test.tsx).
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

// Context-wired so a trigger press exercises the real Tabs →
// onValueChange path (the mode-handback assertion below).
jest.mock("@/components/ui/tabs", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable, View } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  const TabsContext = React.createContext<{
    onValueChange: (v: string) => void;
  } | null>(null);
  return {
    Tabs: ({
      onValueChange,
      children,
    }: {
      onValueChange: (v: string) => void;
      children: React.ReactNode;
    }) =>
      React.createElement(
        TabsContext.Provider,
        { value: { onValueChange } },
        React.createElement(View, null, children),
      ),
    TabsList: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
    TabsTrigger: ({ value, children }: { value: string; children: React.ReactNode }) =>
      React.createElement(
        TabsContext.Consumer,
        null,
        ({ onValueChange }: { onValueChange: (v: string) => void }) =>
          React.createElement(
            Pressable,
            { testID: `mode-${value}`, onPress: () => onValueChange(value) },
            children,
          ),
      ),
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

jest.mock("@/components/issue/manual-create-panel", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    ManualCreatePanel: () =>
      React.createElement(Text, { testID: "manual-create-panel" }, "Manual"),
  };
});

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

jest.mock("@tanstack/react-query", () => ({
  useQuery: ({ queryKey }: { queryKey: string[] }) => {
    switch (queryKey[0]) {
      case "members":
        return { data: [{ user_id: "user-1", role: "member" }] };
      case "agents":
        return { data: [mockFirstAgent, mockSecondAgent], isSuccess: true };
      case "squads":
        return { data: [mockSquad], isSuccess: true };
      case "runtimes":
        return { data: [] };
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

const NewIssueModal =
  jest.requireActual<typeof import("../app/(app)/[workspace]/new-issue")>(
    "../app/(app)/[workspace]/new-issue",
  ).default;
const { useNewIssueDraftStore } = jest.requireActual<
  typeof import("@/data/stores/new-issue-draft-store")
>("@/data/stores/new-issue-draft-store");
const {
  ensureQuickCreateActorMemoryHydrated,
  useQuickCreateActorMemoryHydrationStore,
  useQuickCreateActorMemoryStore,
  useQuickCreatePrefsStore,
} = jest.requireMock<
  typeof import("@/data/stores/quick-create-prefs-store")
>("@/data/stores/quick-create-prefs-store");

const mockEnsureActorMemoryHydrated =
  ensureQuickCreateActorMemoryHydrated as jest.MockedFunction<
    typeof ensureQuickCreateActorMemoryHydrated
  >;

function resetTestState() {
  useNewIssueDraftStore.getState().reset();
  useQuickCreatePrefsStore.setState({ lastMode: "smart" });
  useQuickCreateActorMemoryStore.setState({ byServer: {} });
  useQuickCreateActorMemoryHydrationStore.setState({ status: "ready" });
  mockEnsureActorMemoryHydrated.mockReset();
  mockMutateAsync.mockReset();
  mockMutateAsync.mockResolvedValue({ id: "issue-1" });
}

beforeEach(() => {
  resetTestState();
});

afterEach(async () => {
  await cleanup();
});

describe("NewIssueModal assign-work seed (RUYI-624)", () => {
  it("pre-navigation seed survives the mount reset and forces Smart over remembered Manual", async () => {
    useQuickCreatePrefsStore.getState().setLastMode("manual");
    await act(async () => {
      useNewIssueDraftStore.getState().setSmartActor(seededActor);
    });

    await render(<NewIssueModal />);

    await waitFor(() => {
      expect(useNewIssueDraftStore.getState().smartActor).toEqual(seededActor);
      expect(screen.getByText("agent:agent-second")).toBeTruthy();
    });
    // Smart forced for the visit; the remembered preference untouched.
    expect(screen.queryByTestId("manual-create-panel")).toBeNull();
    expect(useQuickCreatePrefsStore.getState().lastMode).toBe("manual");
  });

  it("submitting the seeded visit targets the assigned agent", async () => {
    useQuickCreatePrefsStore.getState().setLastMode("manual");
    await act(async () => {
      useNewIssueDraftStore.getState().setSmartActor(seededActor);
    });
    await render(<NewIssueModal />);
    await waitFor(() =>
      expect(screen.getByText("agent:agent-second")).toBeTruthy(),
    );

    await fireEvent.changeText(
      screen.getByTestId("quick-create-prompt"),
      "Create this",
    );
    await fireEvent.press(screen.getByTestId("quick-create-submit"));
    await waitFor(() => {
      expect(mockMutateAsync).toHaveBeenCalledTimes(1);
    });
    expect(mockMutateAsync).toHaveBeenLastCalledWith(
      expect.objectContaining({ agent_id: mockSecondAgent.id }),
    );
  });

  it("an explicit tab touch hands mode control back; the seeded actor survives flips", async () => {
    useQuickCreatePrefsStore.getState().setLastMode("manual");
    await act(async () => {
      useNewIssueDraftStore.getState().setSmartActor(seededActor);
    });
    await render(<NewIssueModal />);
    await waitFor(() =>
      expect(screen.getByText("agent:agent-second")).toBeTruthy(),
    );

    await fireEvent.press(screen.getByTestId("mode-manual"));
    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });
    expect(useQuickCreatePrefsStore.getState().lastMode).toBe("manual");

    await fireEvent.press(screen.getByTestId("mode-smart"));
    await waitFor(() => {
      expect(screen.getByText("agent:agent-second")).toBeTruthy();
    });
    expect(useNewIssueDraftStore.getState().smartActor).toEqual(seededActor);
  });

  it("no pre-navigation seed: remembered Manual is respected and no actor is fabricated", async () => {
    useQuickCreatePrefsStore.getState().setLastMode("manual");
    await render(<NewIssueModal />);

    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });
    expect(useNewIssueDraftStore.getState().smartActor).toBeNull();
  });
});
