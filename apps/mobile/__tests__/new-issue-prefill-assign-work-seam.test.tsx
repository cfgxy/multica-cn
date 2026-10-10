import { act, cleanup, render, screen, waitFor } from "@testing-library/react-native";
import type { QuickCreateActorRef } from "@/lib/quick-create";

/**
 * RUYI-605 × RUYI-624 seam: the inbox prefill seed ("edit in the full
 * form") and the assign-work actor seed (`smartActor` + forced Smart)
 * reaching the new-issue screen in the same visit. The two entries are
 * mutually exclusive in real flows, so this is a defensive branch — but
 * nothing prevents both stores from holding a seed, and the QA review of
 * the fusion found it untested (new-issue-prefill covers the RUYI-605
 * side alone, the assign-work suites the RUYI-624 side alone).
 *
 * Fused contract pinned here (see the precedence notes in new-issue.tsx):
 *   - the inbox prefill wins and forces Manual for the visit, even when
 *     the remembered mode is Smart and an actor seed forces Smart;
 *   - the prefill agent is the ONLY assignee write — the assign-work
 *     actor never reaches the manual slot;
 *   - `smartForced` leaves no dormant residue: re-landing on Manual while
 *     the prefill override is active must not carry the actor (the
 *     actor-carry branch is scoped to smartForced visits, and the prefill
 *     consumption clears the flag in the same commit that mounts it).
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
    // Mount-faithful: mirrors useMentionInput's `useState(initialText)` —
    // the description is captured once per mount and later prop changes
    // are ignored (same rationale as new-issue-prefill.test.tsx).
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
const { useNewIssueDraftStore } = jest.requireActual<
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

const ACTOR_SEED: QuickCreateActorRef = {
  type: "agent",
  id: "agent-from-assign-work",
};
const PREFILL_DESCRIPTION = "Deploy the staging build";
const PREFILL_AGENT_ID = "agent-from-prefill";

const originalSetAssignee = useNewIssueDraftStore.getState().setAssignee;

/**
 * Count assignee writes through the store action: the mount reset and the
 * skipped RUYI-79 memory seed do NOT go through setAssignee, so the spy
 * count is exactly the number of explicit slot writes this visit makes.
 */
function spySetAssignee() {
  const spy = jest.fn(originalSetAssignee);
  useNewIssueDraftStore.setState({
    setAssignee: spy as typeof originalSetAssignee,
  });
  return spy;
}

/** Seed both entries in their real-world order (actor first, prefill last). */
function seedCoexistence(agentId: string | null) {
  useNewIssueDraftStore.getState().setSmartActor(ACTOR_SEED);
  seedNewIssuePrefill({ description: PREFILL_DESCRIPTION, agentId });
}

function resetTestState() {
  useNewIssueDraftStore.setState({ setAssignee: originalSetAssignee });
  useNewIssueDraftStore.getState().reset();
  useNewIssuePrefillStore.setState({ seed: null });
  useQuickCreatePrefsStore.setState({ lastMode: "smart" });
  asyncStorage.getItem.mockImplementation(async () => null);
  manualPanelMock.__reset();
  (ManualCreatePanel as jest.Mock).mockClear();
}

beforeEach(resetTestState);

afterEach(async () => {
  await cleanup();
});

describe("NewIssueModal prefill × assign-work seed coexistence (RUYI-605 × RUYI-624)", () => {
  it("both seeds present: prefill wins — Manual mode, prefilled description, prefill agent as the only assignee write", async () => {
    const setAssigneeSpy = spySetAssignee();
    seedCoexistence(PREFILL_AGENT_ID);

    await render(<NewIssueModal />);

    // Mode lands on Manual even though lastMode is Smart AND the actor
    // seed forces Smart — the prefill override outranks both.
    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });
    expect(screen.queryByTestId("quick-create-panel")).toBeNull();
    // The description reaches the panel on its first (and only) mount.
    expect(manualPanelMock.__epochState.epoch).toBe(1);
    expect(latestManualCapture()?.description).toBe(PREFILL_DESCRIPTION);
    // The prefill agent — not the assign-work actor — occupies the slot.
    expect(useNewIssueDraftStore.getState().assignee).toEqual({
      type: "agent",
      id: PREFILL_AGENT_ID,
    });
    expect(setAssigneeSpy).toHaveBeenCalledTimes(1);
    expect(setAssigneeSpy).toHaveBeenCalledWith({
      type: "agent",
      id: PREFILL_AGENT_ID,
    });
    // One-shot prefill consumption is unaffected by the coexistence.
    expect(useNewIssuePrefillStore.getState().seed).toBeNull();
  });

  it("prefill assignee survives a smart↔manual round trip — no actor carry leaks from the assign-work seed", async () => {
    const setAssigneeSpy = spySetAssignee();
    seedCoexistence(PREFILL_AGENT_ID);

    await render(<NewIssueModal />);
    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });
    const versionAtLanding = useNewIssueDraftStore.getState().assigneeVersion;

    await act(async () => {
      tabsMock.__fireModeChange("smart");
    });
    await waitFor(() => {
      expect(screen.getByTestId("quick-create-panel")).toBeTruthy();
    });
    await act(async () => {
      tabsMock.__fireModeChange("manual");
    });
    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });

    // The first explicit switch ended the visit overrides; re-landing on
    // Manual must behave like an unseeded visit — the assign-work actor is
    // not an assignment intent and must not carry into the slot.
    expect(useNewIssueDraftStore.getState().assignee).toEqual({
      type: "agent",
      id: PREFILL_AGENT_ID,
    });
    expect(useNewIssueDraftStore.getState().assigneeVersion).toBe(
      versionAtLanding,
    );
    expect(setAssigneeSpy).toHaveBeenCalledTimes(1);
  });

  it("smartForced leaves no dormant residue: re-landing on Manual never carries the actor", async () => {
    // Description-only prefill: the manual slot stays empty, so a lingering
    // smartForced would pass the carry branch's `!assignee` guard and drop
    // the assign-work actor into the slot. The forced-Smart flag is set and
    // cleared within the same mount commit, so no real tab switch can
    // observe it before clearing it — re-firing the Manual value through
    // the handler IS the probe that reads the flag as the visit left it.
    const setAssigneeSpy = spySetAssignee();
    seedCoexistence(null);

    await render(<NewIssueModal />);
    await waitFor(() => {
      expect(screen.getByTestId("manual-create-panel")).toBeTruthy();
    });
    expect(latestManualCapture()?.description).toBe(PREFILL_DESCRIPTION);
    // Flush the pending async work (the memory backfill runs for an
    // agent-less prefill; with empty storage it must write nothing).
    await act(async () => {
      await Promise.resolve();
    });
    expect(useNewIssueDraftStore.getState().assignee).toBeNull();
    expect(setAssigneeSpy).not.toHaveBeenCalled();

    await act(async () => {
      tabsMock.__fireModeChange("manual");
    });

    expect(screen.queryByTestId("quick-create-panel")).toBeNull();
    expect(useNewIssueDraftStore.getState().assignee).toBeNull();
    expect(setAssigneeSpy).not.toHaveBeenCalled();
  });
});
