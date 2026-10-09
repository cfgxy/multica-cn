// @ts-nocheck
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react-native";
import type { QuickCreateActorRef } from "@/lib/quick-create";

/**
 * RUYI-624 rework — the assign-work entry's fallback to Manual creation.
 *
 * QA defect: on a seeded (assign-work) visit, the fixture daemon reported no
 * CLI version, Smart quick-create was blocked by the version gate, and the
 * user (or the block) lands on the Manual tab — where the seed never
 * arrived, so the issue was created with no assignee at all. Web's
 * `switchToManual` (packages/views/modals/quick-create-issue.tsx) carries
 * the picked actor into the manual assignee slot when the user hasn't
 * picked one; mobile's mode switch lacked that step.
 *
 * Contract pinned here (real ManualCreatePanel + CreateFormAttributeRow,
 * unlike the sibling seed-lifecycle suites which stub the manual panel):
 *
 *   - a seeded visit that switches to Manual lands the actor in the draft
 *     assignee slot via setAssignee (assigneeVersion bump), the chip shows
 *     the agent, and the submit body carries assignee_type/assignee_id;
 *   - a pre-existing assignee (landed memory / explicit pick) is never
 *     overwritten by the seed (web `!assigneeId` guard parity);
 *   - a delayed RUYI-79 memory backfill carrying the stale mount-time
 *     version cannot replace the just-landed seed;
 *   - an unseeded visit keeps its memory-backfill-only semantics — the
 *     smart panel's first-visible fallback actor is NOT an assignment
 *     intent and must not leak into the manual assignee.
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
const firstAgentAssignee = { type: "agent" as const, id: mockFirstAgent.id };

// Holder for the persisted last-assignee payload (zustand persist storage
// shape); referenced inside the AsyncStorage factory below.
const mockStorageState: { lastAssignee: string | null } = { lastAssignee: null };

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: jest.fn(async (_key: string) => mockStorageState.lastAssignee),
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
// onValueChange path (the seeding happens inside the shell's handler).
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

jest.mock("@/components/ui/status-icon", () => ({
  StatusIcon: () => null,
}));

jest.mock("@/components/ui/priority-icon", () => ({
  PriorityIcon: () => null,
}));

jest.mock("@/components/ui/project-icon", () => ({
  ProjectIcon: () => null,
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

// Shared by both panels (manual header button / smart header button);
// only one panel is mounted at a time.
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
        testID: "panel-submit",
      }),
  };
});

jest.mock("@/components/issue/quick-create-attribute-row", () => ({
  QuickCreateAttributeRow: () => null,
}));

jest.mock("@/components/issue/mention-suggestion-bar", () => ({
  MentionSuggestionBar: () => null,
}));

jest.mock("@/components/issue/attachment-zone", () => ({
  AttachmentZone: () => null,
}));

jest.mock("@/components/issue/description-field", () => ({
  DescriptionField: () => null,
}));

jest.mock("@/components/editor/markdown-toolbar", () => ({
  MarkdownToolbar: () => null,
}));

jest.mock("@/components/ui/action-sheet", () => ({
  ActionSheetModal: () => null,
}));

jest.mock("@/components/editor/use-file-attach", () => ({
  useFileAttach: () => ({
    attachments: [],
    pickAndUploadFiles: jest.fn(),
    chooseImageSource: jest.fn(),
    imageSourceModalProps: {},
    removeAttachment: jest.fn(),
    retryAttachment: jest.fn(),
    enqueueAssets: jest.fn(),
    uploading: false,
  }),
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

jest.mock("@tanstack/react-query", () => ({
  queryOptions: (opts: Record<string, unknown>) => opts,
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
      case "issue-statuses":
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
  useCreateIssue: () => ({
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
const { useNewIssueDraftStore, seedDraftAssigneeFromMemory } =
  jest.requireActual<typeof import("@/data/stores/new-issue-draft-store")>(
    "@/data/stores/new-issue-draft-store",
  );
const { useNewIssueLastAssigneeStore } = jest.requireActual<
  typeof import("@/data/stores/new-issue-draft-store")
>("@/data/stores/new-issue-draft-store");
const {
  useQuickCreateActorMemoryHydrationStore,
  useQuickCreateActorMemoryStore,
  useQuickCreatePrefsStore,
} = jest.requireMock<
  typeof import("@/data/stores/quick-create-prefs-store")
>("@/data/stores/quick-create-prefs-store");

function resetTestState() {
  useNewIssueDraftStore.getState().reset();
  useNewIssueLastAssigneeStore.setState({ byServer: {} });
  useQuickCreatePrefsStore.setState({ lastMode: "smart" });
  useQuickCreateActorMemoryStore.setState({ byServer: {} });
  useQuickCreateActorMemoryHydrationStore.setState({ status: "ready" });
  mockStorageState.lastAssignee = null;
  mockMutateAsync.mockReset();
  mockMutateAsync.mockResolvedValue({ id: "issue-1" });
}

/** Seed the pre-navigation actor and wait for the forced Smart panel. */
async function renderSeededVisit() {
  await act(async () => {
    useNewIssueDraftStore.getState().setSmartActor(seededActor);
  });
  render(<NewIssueModal />);
  await waitFor(() =>
    expect(screen.getByText("agent:agent-second")).toBeTruthy(),
  );
}

async function switchToManual() {
  const versionBefore = useNewIssueDraftStore.getState().assigneeVersion;
  await fireEvent.press(screen.getByTestId("mode-manual"));
  await waitFor(() =>
    expect(screen.getByPlaceholderText("Issue title")).toBeTruthy(),
  );
  return versionBefore;
}

beforeEach(() => {
  resetTestState();
});

afterEach(async () => {
  await cleanup();
});

describe("NewIssueModal assign-work manual fallback (RUYI-624 rework)", () => {
  it("seeded visit: switching to Manual lands the actor in the assignee slot; chip and submit body follow", async () => {
    await renderSeededVisit();

    const versionBefore = await switchToManual();
    expect(useNewIssueDraftStore.getState().assignee).toEqual(seededActor);
    // Landed through setAssignee — exactly one version bump, so a still
    // pending RUYI-79 memory backfill (stale version) cannot overwrite it.
    expect(useNewIssueDraftStore.getState().assigneeVersion).toBe(
      versionBefore + 1,
    );
    expect(screen.getByText("agent:agent-second")).toBeTruthy();

    await fireEvent.changeText(
      screen.getByPlaceholderText("Issue title"),
      "Manual task",
    );
    await fireEvent.press(screen.getByTestId("panel-submit"));
    await waitFor(() => {
      expect(mockMutateAsync).toHaveBeenCalledTimes(1);
    });
    expect(mockMutateAsync).toHaveBeenLastCalledWith(
      expect.objectContaining({
        title: "Manual task",
        assignee_type: "agent",
        assignee_id: mockSecondAgent.id,
      }),
    );
  });

  it("seeded visit: a landed memory assignee is not overwritten by the seed", async () => {
    // The mount reset wipes any hand-set draft value by design — the only
    // way an assignee exists before the user picks one is the RUYI-79
    // memory backfill landing at mount. Web parity: that memory-seeded
    // assignee counts as "picked" and blocks the actor seed.
    mockStorageState.lastAssignee = JSON.stringify({
      state: { byServer: { "server-1": { "workspace-a": firstAgentAssignee } } },
      version: 0,
    });
    await useNewIssueLastAssigneeStore.persist.rehydrate();
    await renderSeededVisit();
    expect(useNewIssueDraftStore.getState().assignee).toEqual(
      firstAgentAssignee,
    );

    const versionBefore = await switchToManual();
    expect(useNewIssueDraftStore.getState().assignee).toEqual(
      firstAgentAssignee,
    );
    expect(useNewIssueDraftStore.getState().assigneeVersion).toBe(
      versionBefore,
    );
    expect(screen.getByText("agent:agent-first")).toBeTruthy();
  });

  it("an explicit re-pick after landing survives the smart↔manual round-trip", async () => {
    await renderSeededVisit();

    await switchToManual();
    expect(useNewIssueDraftStore.getState().assignee).toEqual(seededActor);

    // The assignee picker route writes through the same setAssignee.
    await act(async () => {
      useNewIssueDraftStore.getState().setAssignee(firstAgentAssignee);
    });
    await fireEvent.press(screen.getByTestId("mode-smart"));
    await waitFor(() =>
      expect(screen.getByText("agent:agent-second")).toBeTruthy(),
    );
    await fireEvent.press(screen.getByTestId("mode-manual"));
    await waitFor(() =>
      expect(screen.getByPlaceholderText("Issue title")).toBeTruthy(),
    );

    expect(useNewIssueDraftStore.getState().assignee).toEqual(
      firstAgentAssignee,
    );
    expect(screen.getByText("agent:agent-first")).toBeTruthy();
  });

  it("delayed last-assignee memory cannot overwrite the landed seed (assigneeVersion guard)", async () => {
    await renderSeededVisit();
    const versionAtMount = useNewIssueDraftStore.getState().assigneeVersion;

    await switchToManual();
    expect(useNewIssueDraftStore.getState().assignee).toEqual(seededActor);

    // The backfill captured at mount finally resolves, carrying the stale
    // mount-time version — it must not replace the just-landed seed.
    useNewIssueLastAssigneeStore.setState({
      byServer: { "server-1": { "workspace-a": firstAgentAssignee } },
    });
    const seeded = await seedDraftAssigneeFromMemory(
      "server-1",
      "workspace-a",
      versionAtMount,
    );
    expect(seeded).toBe(false);
    expect(useNewIssueDraftStore.getState().assignee).toEqual(seededActor);

    // Control: the same call with the current version applies — the guard
    // (not the memory store) is what protected the seed above.
    const applied = await seedDraftAssigneeFromMemory(
      "server-1",
      "workspace-a",
      useNewIssueDraftStore.getState().assigneeVersion,
    );
    expect(applied).toBe(true);
    expect(useNewIssueDraftStore.getState().assignee).toEqual(
      firstAgentAssignee,
    );
  });

  it("unseeded visit: memory backfill lands and the mode switch leaves it untouched", async () => {
    mockStorageState.lastAssignee = JSON.stringify({
      state: { byServer: { "server-1": { "workspace-a": firstAgentAssignee } } },
      version: 0,
    });
    await useNewIssueLastAssigneeStore.persist.rehydrate();

    render(<NewIssueModal />);
    await waitFor(() =>
      expect(useNewIssueDraftStore.getState().assignee).toEqual(
        firstAgentAssignee,
      ),
    );

    await switchToManual();
    expect(useNewIssueDraftStore.getState().assignee).toEqual(
      firstAgentAssignee,
    );
    expect(screen.getByText("agent:agent-first")).toBeTruthy();
  });

  it("unseeded visit with no memory: the smart panel's fallback actor is not an assignment intent", async () => {
    render(<NewIssueModal />);
    // Smart mode auto-picks the first visible agent into smartActor —
    // pinned by the seed-lifecycle suite; it must NOT become the manual
    // assignee when the user only flips tabs.
    await waitFor(() =>
      expect(screen.getByText(`agent:${mockFirstAgent.id}`)).toBeTruthy(),
    );

    await switchToManual();
    expect(useNewIssueDraftStore.getState().assignee).toBeNull();
    expect(screen.queryByText(`agent:${mockFirstAgent.id}`)).toBeNull();
  });
});
