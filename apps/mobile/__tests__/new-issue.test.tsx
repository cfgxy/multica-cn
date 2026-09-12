import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react-native";
import type { QuickCreateActorRef } from "@/lib/quick-create";

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

const mockFirstAgentActor = { type: "agent" as const, id: mockFirstAgent.id };
const mockSecondAgentActor = { type: "agent" as const, id: mockSecondAgent.id };
const mockSquadActor = { type: "squad" as const, id: mockSquad.id };

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

jest.mock("@/components/ui/tabs", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable, View } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    Tabs: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
    TabsList: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
    TabsTrigger: ({ children, value }: { children: React.ReactNode; value: string }) =>
      React.createElement(Pressable, { testID: `mode-${value}` }, children),
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
const { router } = jest.requireMock<typeof import("expo-router")>("expo-router");
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
const mockRouterBack = router.back as jest.Mock;
const mockRouterPush = router.push as jest.Mock;

function resetTestState() {
  useNewIssueDraftStore.getState().reset();
  useQuickCreatePrefsStore.setState({ lastMode: "smart" });
  useQuickCreateActorMemoryStore.setState({ byServer: {} });
  useQuickCreateActorMemoryHydrationStore.setState({ status: "ready" });
  mockEnsureActorMemoryHydrated.mockReset();
  mockMutateAsync.mockReset();
  mockMutateAsync.mockResolvedValue({ id: "issue-1" });
  mockRouterBack.mockReset();
  mockRouterPush.mockReset();
}

async function submitAndClose(actor: QuickCreateActorRef) {
  await act(() => {
    useNewIssueDraftStore.getState().setSmartActor(actor);
  });
  await waitFor(() => {
    expect(useNewIssueDraftStore.getState().smartActor).toEqual(actor);
    expect(screen.getByText(`${actor.type}:${actor.id}`)).toBeTruthy();
  });

  await fireEvent.changeText(
    screen.getByTestId("quick-create-prompt"),
    "Create this",
  );
  await waitFor(() => {
    expect(screen.getByTestId("quick-create-prompt").props.value).toBe(
      "Create this",
    );
  });
  await fireEvent.press(screen.getByTestId("quick-create-submit"));
  await waitFor(() => {
    expect(mockMutateAsync).toHaveBeenCalledTimes(1);
  });
  await waitFor(() => expect(mockRouterBack).toHaveBeenCalledTimes(1));
}

async function expectRestoredAfterSameProcessReopen(
  actor: QuickCreateActorRef,
  expectedBody: Record<string, string>,
) {
  const firstVisit = await render(<NewIssueModal />);
  await submitAndClose(actor);
  expect(mockMutateAsync).toHaveBeenLastCalledWith({
    ...expectedBody,
    prompt: "Create this",
  });

  await firstVisit.unmount();
  await waitFor(() => {
    expect(useNewIssueDraftStore.getState().smartActor).toBeNull();
  });

  mockMutateAsync.mockClear();
  await render(<NewIssueModal />);
  await waitFor(() => {
    expect(useNewIssueDraftStore.getState().smartActor).toEqual(actor);
    expect(screen.getByText(`${actor.type}:${actor.id}`)).toBeTruthy();
  });

  await fireEvent.changeText(
    screen.getByTestId("quick-create-prompt"),
    "Create again",
  );
  await waitFor(() => {
    expect(screen.getByTestId("quick-create-prompt").props.value).toBe(
      "Create again",
    );
  });
  await fireEvent.press(screen.getByTestId("quick-create-submit"));
  await waitFor(() => {
    expect(mockMutateAsync).toHaveBeenLastCalledWith({
      ...expectedBody,
      prompt: "Create again",
    });
  });
}

beforeEach(() => {
  resetTestState();
});

afterEach(async () => {
  await cleanup();
});

describe("NewIssueModal smart actor lifecycle", () => {
  it("restores a remembered squad after a successful create and same-process reopen", async () => {
    await expectRestoredAfterSameProcessReopen(mockSquadActor, {
      squad_id: mockSquad.id,
    });
  });

  it("restores a remembered non-first agent after a successful create and same-process reopen", async () => {
    await expectRestoredAfterSameProcessReopen(mockSecondAgentActor, {
      agent_id: mockSecondAgent.id,
    });
  });

  it("defaults to the first visible agent when there is no history", async () => {
    await render(<NewIssueModal />);

    await waitFor(() => {
      expect(useNewIssueDraftStore.getState().smartActor).toEqual(
        mockFirstAgentActor,
      );
      expect(screen.getByText(`agent:${mockFirstAgent.id}`)).toBeTruthy();
    });
  });

  it("keeps an explicit actor through delayed initialization and a Smart/Manual mode switch", async () => {
    let finishHydration: (() => void) | undefined;
    mockEnsureActorMemoryHydrated.mockImplementation(
      () =>
        new Promise<"ready">((resolve) => {
          finishHydration = () => {
            useQuickCreateActorMemoryHydrationStore
              .getState()
              .setStatus("ready");
            resolve("ready");
          };
        }),
    );
    useQuickCreateActorMemoryHydrationStore.setState({ status: "pending" });

    await render(<NewIssueModal />);
    await waitFor(() => expect(mockEnsureActorMemoryHydrated).toHaveBeenCalled());

    const explicitActor = mockSecondAgentActor;
    await act(() => {
      useNewIssueDraftStore.getState().setSmartActor(explicitActor);
      finishHydration?.();
    });
    await waitFor(() => {
      expect(useNewIssueDraftStore.getState().smartActor).toEqual(explicitActor);
    });

    await act(() => {
      useQuickCreatePrefsStore.getState().setLastMode("manual");
    });
    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });

    await act(() => {
      useQuickCreatePrefsStore.getState().setLastMode("smart");
    });
    await waitFor(() => {
      expect(useNewIssueDraftStore.getState().smartActor).toEqual(explicitActor);
    });
  });
});
