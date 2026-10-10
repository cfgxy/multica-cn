import { act, cleanup, render, screen, waitFor } from "@testing-library/react-native";
import type { AssigneeValue } from "@/components/issue/pickers/assignee-picker-body";

/**
 * RUYI-605: the new-issue screen's consumption of the one-shot inbox
 * prefill ("edit in the full form" from a quick-create outcome detail).
 *
 * Contract under test:
 *   - a pending seed forces the manual panel for this visit (web parity:
 *     the create-issue registry opens `initialMode="manual"` without
 *     changing the remembered mode preference — the override clears on the
 *     first tab switch);
 *   - the seed lands in a CLEAN draft (consumed after the mount reset) and
 *     reaches ManualCreatePanel as `initialDescription`;
 *   - when the remembered mode is already manual, the seed reaches the panel
 *     via a pre-paint REMOUNT — the panel's mount-only `useState(initialText)`
 *     cannot absorb a late prop update;
 *   - an explicit agent prefill wins over the last-assignee memory
 *     (RUYI-79), while a description-only prefill leaves the memory seed
 *     intact;
 *   - the seed is consumed exactly once and absent seeds change nothing.
 */

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
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
      Screen: () => null,
    },
    router: { back: jest.fn(), push: jest.fn() },
  };
});

jest.mock("@/components/ui/tabs", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable, View } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  let changeHandler: ((v: string) => void) | null = null;
  return {
    Tabs: ({
      children,
      onValueChange,
    }: {
      children: React.ReactNode;
      onValueChange: (v: string) => void;
    }) => {
      changeHandler = onValueChange;
      return React.createElement(View, null, children);
    },
    TabsList: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
    TabsTrigger: ({
      children,
      value,
    }: {
      children: React.ReactNode;
      value: string;
    }) => React.createElement(Pressable, { testID: `mode-${value}` }, children),
    __fireModeChange: (v: string) => changeHandler?.(v),
  };
});

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return { Text };
});

jest.mock("@/components/issue/manual-create-panel", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  const state = { epoch: 0 };
  const captures: Array<{ epoch: number; description: string | undefined }> =
    [];
  function ManualCreatePanelMock(props: { initialDescription?: string }) {
    // Mount-faithful: mirrors useMentionInput's `useState(initialText)` — the
    // description is captured once per mount and later prop changes are
    // ignored. A transparent prop recorder would hide the RUYI-605 remount
    // contract (lastMode "manual" mounts before the seed state exists, so
    // the seed must arrive via a pre-paint remount, not a prop update).
    const [captured] = React.useState(props.initialDescription);
    const [epoch] = React.useState(() => {
      state.epoch += 1;
      return state.epoch;
    });
    captures.push({ epoch, description: captured });
    return React.createElement(
      Text,
      { testID: "manual-create-panel" },
      captured ?? "",
    );
  }
  return {
    __esModule: true,
    ManualCreatePanel: Object.assign(jest.fn(ManualCreatePanelMock), {
      __captures: captures,
      __epochState: state,
      __reset: () => {
        captures.length = 0;
        state.epoch = 0;
      },
    }),
  };
});

jest.mock("@/components/issue/quick-create-panel", () => ({
  QuickCreatePanel: () => {
    const React = jest.requireActual<typeof import("react")>("react");
    const { Text } = jest.requireActual<typeof import("react-native")>(
      "react-native",
    );
    return React.createElement(Text, { testID: "quick-create-panel" }, "Smart");
  },
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

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key: string, fallback: string) => fallback,
  }),
}));

const NewIssueModal =
  jest.requireActual<typeof import("../app/(app)/[workspace]/new-issue")>(
    "../app/(app)/[workspace]/new-issue",
  ).default;
const tabsMock = jest.requireMock("@/components/ui/tabs") as {
  __fireModeChange: (v: string) => void;
};
const { ManualCreatePanel } = jest.requireMock<
  typeof import("@/components/issue/manual-create-panel")
>("@/components/issue/manual-create-panel");
const manualPanelMock = ManualCreatePanel as jest.Mock & {
  __captures: Array<{ epoch: number; description: string | undefined }>;
  __epochState: { epoch: number };
  __reset: () => void;
};

/** The description captured on the most recently mounted panel instance. */
function latestManualCapture() {
  return manualPanelMock.__captures.reduce<
    { epoch: number; description: string | undefined } | null
  >((acc, cur) => (cur.epoch > (acc?.epoch ?? 0) ? cur : acc), null);
}
const asyncStorage = jest.requireMock("@react-native-async-storage/async-storage")
  .default as { getItem: jest.Mock };
const {
  useNewIssueDraftStore,
  useNewIssueLastAssigneeStore,
} = jest.requireActual<
  typeof import("@/data/stores/new-issue-draft-store")
>("@/data/stores/new-issue-draft-store");
const {
  seedNewIssuePrefill,
  useNewIssuePrefillStore,
} = jest.requireActual<
  typeof import("@/data/stores/new-issue-prefill-store")
>("@/data/stores/new-issue-prefill-store");
const { useQuickCreatePrefsStore } = jest.requireActual<
  typeof import("@/data/stores/quick-create-prefs-store")
>("@/data/stores/quick-create-prefs-store");

const MEMORY_KEY = "multica_mobile_new_issue_last_assignee";

function rememberLastAssignee(value: AssigneeValue) {
  useNewIssueLastAssigneeStore.setState({
    byServer: { "server-1": { "workspace-a": value } },
  });
}

function resetTestState() {
  useNewIssueDraftStore.getState().reset();
  useNewIssuePrefillStore.setState({ seed: null });
  useQuickCreatePrefsStore.setState({ lastMode: "smart" });
  useNewIssueLastAssigneeStore.setState({ byServer: {} });
  asyncStorage.getItem.mockImplementation(async () => null);
  manualPanelMock.__reset();
  (ManualCreatePanel as jest.Mock).mockClear();
}

beforeEach(resetTestState);

afterEach(async () => {
  await cleanup();
});

describe("NewIssueModal inbox prefill (RUYI-605)", () => {
  it("opens the manual panel with the seeded description and agent assignee", async () => {
    seedNewIssuePrefill({
      description: "Deploy the staging build",
      agentId: "agent-1",
    });

    render(<NewIssueModal />);

    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });
    expect(screen.queryByTestId("quick-create-panel")).toBeNull();
    expect(useNewIssueDraftStore.getState().assignee).toEqual({
      type: "agent",
      id: "agent-1",
    });
    expect(
      (ManualCreatePanel as jest.Mock).mock.calls.at(-1)![0],
    ).toMatchObject({ initialDescription: "Deploy the staging build" });
    // One-shot: the seed is consumed by the visit.
    expect(useNewIssuePrefillStore.getState().seed).toBeNull();
  });

  it("remounts the manual panel with the seed when the remembered mode is manual", async () => {
    // Regression: with lastMode "manual" the panel mounts on the FIRST
    // render, where the prefill state is still null — a late prop update
    // cannot reach its mount-only useState(initialText). The seed must land
    // via a pre-paint remount (key change), yielding two mount epochs with
    // the seeded description captured on the second.
    useQuickCreatePrefsStore.setState({ lastMode: "manual" });
    seedNewIssuePrefill({
      description: "Deploy the staging build",
      agentId: "agent-1",
    });

    render(<NewIssueModal />);

    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });
    expect(manualPanelMock.__epochState.epoch).toBe(2);
    expect(latestManualCapture()?.description).toBe("Deploy the staging build");
    expect(useNewIssueDraftStore.getState().assignee).toEqual({
      type: "agent",
      id: "agent-1",
    });
    expect(useNewIssuePrefillStore.getState().seed).toBeNull();
  });

  it("keeps the explicit agent prefill over the last-assignee memory", async () => {
    rememberLastAssignee({ type: "member", id: "user-1" });
    seedNewIssuePrefill({ description: "Fix the flaky test", agentId: "agent-1" });

    render(<NewIssueModal />);

    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });
    // Flush pending async work (memory hydration would land here if it ran).
    await act(async () => {
      await Promise.resolve();
    });
    expect(useNewIssueDraftStore.getState().assignee).toEqual({
      type: "agent",
      id: "agent-1",
    });
  });

  it("still applies the last-assignee memory for a description-only prefill", async () => {
    asyncStorage.getItem.mockImplementation(async (key: string) =>
      key === MEMORY_KEY
        ? JSON.stringify({
            state: {
              byServer: { "server-1": { "workspace-a": { type: "member", id: "user-1" } } },
            },
            version: 0,
          })
        : null,
    );
    seedNewIssuePrefill({ description: "Prompt without an agent", agentId: null });

    // The persist store auto-hydrates once at import with the empty default
    // mock; data mocked after that point only lands via an explicit
    // rehydrate before rendering.
    await useNewIssueLastAssigneeStore.persist.rehydrate();
    render(<NewIssueModal />);

    await waitFor(() => {
      expect(useNewIssueDraftStore.getState().assignee).toEqual({
        type: "member",
        id: "user-1",
      });
    });
    expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
  });

  it("falls back to the remembered mode after the user switches tabs once", async () => {
    seedNewIssuePrefill({ description: "Seed", agentId: null });

    render(<NewIssueModal />);
    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });

    await act(async () => {
      tabsMock.__fireModeChange("smart");
    });
    await waitFor(() => {
      expect(screen.getByTestId("quick-create-panel")).toBeTruthy();
    });
    expect(useQuickCreatePrefsStore.getState().lastMode).toBe("smart");
  });

  it("changes nothing when no seed is pending", async () => {
    asyncStorage.getItem.mockImplementation(async (key: string) =>
      key === MEMORY_KEY
        ? JSON.stringify({
            state: {
              byServer: { "server-1": { "workspace-a": { type: "member", id: "user-1" } } },
            },
            version: 0,
          })
        : null,
    );
    await useNewIssueLastAssigneeStore.persist.rehydrate();

    render(<NewIssueModal />);

    // Default flow: smart panel, memory-seeded assignee, empty description.
    await waitFor(() => {
      expect(screen.getByTestId("quick-create-panel")).toBeTruthy();
    });
    await waitFor(() => {
      expect(useNewIssueDraftStore.getState().assignee).toEqual({
        type: "member",
        id: "user-1",
      });
    });
    expect(ManualCreatePanel).not.toHaveBeenCalled();
  });
});
