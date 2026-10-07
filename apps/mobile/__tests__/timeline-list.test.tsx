// @ts-nocheck

import React, { useSyncExternalStore } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react-native";
import { Platform, Pressable, Text, View } from "react-native";
import type { Issue, IssueDecision, TimelineEntry } from "@multica/core/types";
import type { TimelineSortMode } from "@multica/core/issues/timeline-sort";

const mockReact = React;
const mockText = Text;

const mockTimelineListeners = new Set<() => void>();
const mockTimelineState = {
  hintSeen: true,
  setHintSeen() {
    mockTimelineState.hintSeen = true;
    for (const listener of mockTimelineListeners) listener();
  },
};

function mockUseTimelineStore<T>(selector: (state: typeof mockTimelineState) => T): T {
  return useSyncExternalStore(
    (listener) => {
      mockTimelineListeners.add(listener);
      return () => mockTimelineListeners.delete(listener);
    },
    () => selector(mockTimelineState),
  );
}

Object.assign(mockUseTimelineStore, {
  getState: () => mockTimelineState,
});

const mockCommentFocusState = {
  focus: null,
  expandedRoots: {} as Record<string, Set<string>>,
  expandRoot: jest.fn(),
  resetIssue: jest.fn(),
  setStatus: jest.fn(),
};

function mockUseCommentFocusStore<T>(selector: (state: typeof mockCommentFocusState) => T): T {
  return selector(mockCommentFocusState);
}

Object.assign(mockUseCommentFocusStore, {
  getState: () => mockCommentFocusState,
});

const mockLastViewedState = {
  getLastViewed: jest.fn(() => null),
  markViewed: jest.fn(),
};

function mockUseLastViewedStore<T>(selector: (state: typeof mockLastViewedState) => T): T {
  return selector(mockLastViewedState);
}

Object.assign(mockUseLastViewedStore, {
  getState: () => mockLastViewedState,
});

jest.mock("@shopify/flash-list", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    FlashList: React.forwardRef((props: Record<string, unknown>, ref) => {
      React.useImperativeHandle(ref, () => ({
        getFirstItemOffset: () => 0,
        getLayout: () => null,
        scrollToEnd: jest.fn(),
        scrollToIndex: jest.fn(),
        scrollToOffset: jest.fn(),
      }));
      const data = props.data as Array<{ entry: { id: string } }>;
      const renderItem = props.renderItem as (mockArgs: { item: unknown; index: number }) => React.ReactNode;
      return React.createElement(
        View,
        { testID: "timeline-list" },
        props.ListHeaderComponent as React.ReactNode,
        data.map((item, index) =>
          React.createElement(
            View,
            { key: item.entry.id, testID: `timeline-row-${item.entry.id}` },
            renderItem({ item, index }),
          ),
        ),
      );
    }),
  };
});

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

// The decision-card + decisions-query import chain (RUYI-345) reaches
// @/data/api, whose module scope reads EXPO_PUBLIC_API_URL — stand in the
// decision methods only; everything else in this suite stays mocked above.
jest.mock("@/data/api", () => ({
  api: {
    listIssueDecisions: () => Promise.resolve([]),
    answerIssueDecision: () => Promise.resolve({}),
    cancelIssueDecision: () => Promise.resolve({}),
    listMembers: () => Promise.resolve([]),
    listAgents: () => Promise.resolve([]),
    listSquads: () => Promise.resolve([]),
  },
}));

jest.mock("expo-haptics", () => ({
  impactAsync: jest.fn(),
}));

jest.mock("@/components/issue/decision-card", () => ({
  DecisionCard: ({ decision }: { decision: { id: string } }) =>
    mockReact.createElement(mockText, null, `decision-card:${decision.id}`),
}));

jest.mock("@/components/issue/decision-batch-bar", () => ({
  DecisionBatchBar: ({ open }: { open: Array<{ id: string }> }) =>
    mockReact.createElement(
      mockText,
      null,
      `decision-batch-bar:${open.map((card) => card.id).join(",")}`,
    ),
}));

// Decision cards reach the list through the component's own useQuery; tests
// swap this bag to control what interleaveDecisions merges into the rows.
const mockDecisionsData: { data: IssueDecision[] } = { data: [] };

jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: mockDecisionsData.data }),
  // passthrough: the decisions options builder (RUYI-345) just labels an
  // options object; the stubbed useQuery above never runs its queryFn.
  queryOptions: (opts: unknown) => opts,
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("@/components/ui/tabs", () => {
  const React = jest.requireActual("react");
  const { Pressable, View } = jest.requireActual("react-native");
  const Context = React.createContext(() => undefined);
  return {
    Tabs: (mockProps) =>
      React.createElement(Context.Provider, { value: mockProps.onValueChange }, mockProps.children),
    TabsList: (mockProps) => React.createElement(View, null, mockProps.children),
    TabsTrigger: (mockProps) => {
      const onValueChange = React.useContext(Context);
      return React.createElement(
        Pressable,
        { onPress: () => onValueChange(mockProps.value), testID: `timeline-sort-${mockProps.value}` },
        mockProps.children,
      );
    },
  };
});

jest.mock("@/components/issue/issue-header-card", () => ({ IssueHeaderCard: () => null }));
jest.mock("@/components/issue/issue-description", () => ({ IssueDescription: () => null }));
jest.mock("@/components/issue/issue-reaction-row", () => ({ IssueReactionRow: () => null }));
jest.mock("@/components/issue/activity-row", () => ({
  ActivityRow: ({ entry }: { entry: TimelineEntry }) =>
    mockReact.createElement(mockText, null, entry.id),
}));
jest.mock("@/components/issue/comment-card", () => ({
  CommentCard: ({ entry, replies }: { entry: TimelineEntry; replies: TimelineEntry[] }) =>
    mockReact.createElement(
      mockText,
      null,
      `${entry.id}:${replies.map((reply) => reply.id).join(",")}`,
    ),
}));

jest.mock("@/data/stores/last-viewed-store", () => ({
  useLastViewedStore: mockUseLastViewedStore,
}));
jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: null }) => unknown) =>
    selector({ currentWorkspaceId: null }),
}));
jest.mock("@/data/queries/issues", () => ({
  issueAttachmentsOptions: () => ({ queryKey: ["attachments"] }),
}));
const mockCommentSelectState = {
  selectingId: null as string | null,
  clear: jest.fn(),
};

jest.mock("@/data/comment-select-store", () => ({
  useCommentSelectStore: Object.assign(
    (selector: (state: typeof mockCommentSelectState) => unknown) =>
      selector(mockCommentSelectState),
    { getState: () => mockCommentSelectState },
  ),
}));
jest.mock("@/data/stores/timeline-sort-store", () => ({
  useTimelineSortStore: mockUseTimelineStore,
}));
jest.mock("@/data/stores/comment-focus-store", () => ({
  useCommentFocusStore: mockUseCommentFocusStore,
}));
jest.mock("@/data/stores/issue-read-state-store", () => ({
  useIssueReadStateStore: {
    getState: () => ({ isRead: () => false, markRead: jest.fn() }),
  },
}));

jest.mock("@/lib/markdown/image-sequence", () => ({
  ImageSequenceProvider: ({ children }: { children: React.ReactNode }) => children,
}));
jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));
jest.mock("@/lib/theme", () => ({
  THEME: { light: { mutedForeground: "#000000" } },
}));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback: string) => fallback }),
}));
jest.mock("@/lib/scroll-activity", () => ({
  ScrollActivityTracker: class {
    dispose() {}
    notifyScroll() {}
  },
}));
jest.mock("@/lib/comment-anchor-context", () => ({
  CommentAnchorProvider: ({ children }: { children: React.ReactNode }) => children,
}));

import { TimelineList } from "@/components/issue/timeline-list";

const issue = {
  id: "issue-1",
  identifier: "RUYI-136",
  description: "",
} as Issue;

function comment(id: string, created_at: string, parent_id?: string): TimelineEntry {
  return {
    type: "comment",
    id,
    actor_type: "member",
    actor_id: "member-1",
    created_at,
    parent_id,
  };
}

function TimelineHarness({
  entries,
  truncatedKinds,
}: {
  entries: TimelineEntry[];
  truncatedKinds: readonly ("activity" | "comment")[];
}) {
  const [mode, setMode] = React.useState<TimelineSortMode>("recent-comment");
  return (
    <TimelineList
      issue={issue}
      entries={entries}
      mode={mode}
      onModeChange={setMode}
      truncatedKinds={truncatedKinds}
      timelineLoading={false}
      refreshing={false}
      onRefresh={jest.fn()}
    />
  );
}

async function renderTimeline(
  entries: TimelineEntry[],
  truncatedKinds: readonly ("activity" | "comment")[] = [],
) {
  return render(
    <TimelineHarness
      entries={entries}
      truncatedKinds={truncatedKinds}
    />,
  );
}

describe("TimelineList sorting and truncation", () => {
  beforeEach(() => {
    mockTimelineState.hintSeen = true;
    mockLastViewedState.getLastViewed.mockReturnValue(null);
    mockLastViewedState.markViewed.mockClear();
    mockCommentFocusState.expandRoot.mockClear();
    mockCommentFocusState.resetIssue.mockClear();
    mockCommentFocusState.setStatus.mockClear();
  });

  it("renders the recent-comment order, sorting controls, and truncation hint", async () => {
    const rootA = comment("root-a", "2026-09-05T09:00:00Z");
    const rootB = comment("root-b", "2026-09-05T10:00:00Z");
    const replyA = comment("reply-a", "2026-09-05T11:00:00Z", rootA.id);

    await renderTimeline([rootA, rootB, replyA], ["comment"]);

    expect(screen.getByText("Comment order")).toBeTruthy();
    expect(screen.getByText("Earlier timeline content has not been loaded.")).toBeTruthy();
    expect(screen.getAllByTestId(/timeline-row-/).map((node) => node.props.testID)).toEqual([
      "timeline-row-root-b",
      "timeline-row-root-a",
    ]);
  });

  it("switches the visible thread order to creation time", async () => {
    const rootA = comment("root-a", "2026-09-05T09:00:00Z");
    const rootB = comment("root-b", "2026-09-05T10:00:00Z");
    const replyA = comment("reply-a", "2026-09-05T11:00:00Z", rootA.id);

    await renderTimeline([rootA, rootB, replyA]);

    await act(async () => {
      fireEvent.press(screen.getByTestId("timeline-sort-created"));
    });

    expect(screen.getAllByTestId(/timeline-row-/).map((node) => node.props.testID)).toEqual([
      "timeline-row-root-a",
      "timeline-row-root-b",
    ]);
    expect(screen.getByText("root-a:reply-a")).toBeTruthy();
    expect(screen.getByText("root-b:")).toBeTruthy();
  });

  it.each([
    ["an empty timeline", []],
    ["a single thread", [comment("root-a", "2026-09-05T09:00:00Z")]],
  ] as const)("hides sorting controls for %s", async (_label, entries) => {
    await renderTimeline([...entries]);

    expect(screen.queryByText("Comment order")).toBeNull();
  });

  it("hides the truncation hint when the fetch snapshot is complete", async () => {
    await renderTimeline([
      comment("root-a", "2026-09-05T09:00:00Z"),
      comment("root-b", "2026-09-05T10:00:00Z"),
    ]);

    expect(
      screen.queryByText("Earlier timeline content has not been loaded."),
    ).toBeNull();
  });
});

describe("selection-mode dismiss layer (RUYI-416)", () => {
  const originalOS = Platform.OS;

  beforeEach(() => {
    mockCommentSelectState.clear.mockClear();
  });

  afterEach(() => {
    Object.defineProperty(Platform, "OS", {
      value: originalOS,
      configurable: true,
    });
  });

  it("iOS: a short tap outside the selected comment still clears selection mode", async () => {
    Object.defineProperty(Platform, "OS", {
      value: "ios",
      configurable: true,
    });
    mockCommentSelectState.selectingId = "root-a";
    try {
      await renderTimeline([comment("root-a", "2026-09-05T09:00:00Z")]);

      expect(mockCommentSelectState.clear).not.toHaveBeenCalled();
      fireEvent.press(screen.getByTestId("timeline-select-dismiss-layer"));
      expect(mockCommentSelectState.clear).toHaveBeenCalledTimes(1);
    } finally {
      mockCommentSelectState.selectingId = null;
    }
  });

  it("Android: while selecting, the dismiss layer declines the responder claim so nothing JS-side can wipe the mode", async () => {
    // The rework defect: with the layer enabled during selection mode, its
    // Pressability claim (onStartShouldSetResponder → true) starved the
    // native TextView selection pipeline — no selection ever appeared —
    // and the release fired the compensating onPress → clear(), wiping
    // the fresh mode. With the layer disabled on Android, Pressability
    // still attaches its handlers but the claim handler returns false:
    // no JS view enters the negotiation, the native long-press owns the
    // touch, and the mode survives. Exit paths are scroll and long-press
    // another body.
    Object.defineProperty(Platform, "OS", {
      value: "android",
      configurable: true,
    });
    mockCommentSelectState.selectingId = "root-a";
    try {
      await renderTimeline([comment("root-a", "2026-09-05T09:00:00Z")]);

      const layer = screen.getByTestId("timeline-select-dismiss-layer");
      expect(layer.props.onStartShouldSetResponder()).toBe(false);
      expect(mockCommentSelectState.clear).not.toHaveBeenCalled();
    } finally {
      mockCommentSelectState.selectingId = null;
    }
  });

  it("Android: outside selection mode the layer still declines (layout-only wrapper)", async () => {
    Object.defineProperty(Platform, "OS", {
      value: "android",
      configurable: true,
    });
    try {
      await renderTimeline([comment("root-a", "2026-09-05T09:00:00Z")]);

      const layer = screen.getByTestId("timeline-select-dismiss-layer");
      expect(layer.props.onStartShouldSetResponder()).toBe(false);
      expect(mockCommentSelectState.clear).not.toHaveBeenCalled();
    } finally {
      mockCommentSelectState.selectingId = null;
    }
  });
});

describe("decision batch bar placement (RUYI-534)", () => {
  function decisionCard(id: string, createdAt: string): IssueDecision {
    return {
      id,
      issue_id: issue.id,
      source_comment_id: null,
      question: "q",
      options: [{ label: "A" }, { label: "B" }],
      multi_select: false,
      recommended_indices: [],
      status: "open",
      selected_indices: [],
      answered_by_type: null,
      answered_by_id: null,
      answered_at: null,
      answer_comment_id: null,
      created_by_type: "agent",
      created_by_id: "a-1",
      created_at: createdAt,
      updated_at: createdAt,
    };
  }

  afterEach(() => {
    mockDecisionsData.data = [];
  });

  it("appends the batch bar after all comments and cards when two or more are open", async () => {
    mockDecisionsData.data = [
      decisionCard("d-2", "2026-09-05T12:00:00Z"),
      decisionCard("d-1", "2026-09-05T08:00:00Z"),
    ];

    await renderTimeline([
      comment("root-a", "2026-09-05T09:00:00Z"),
      comment("root-b", "2026-09-05T10:00:00Z"),
    ]);

    expect(
      screen.getAllByTestId(/timeline-row-/).map((node) => node.props.testID),
    ).toEqual([
      "timeline-row-root-a",
      "timeline-row-d-1",
      "timeline-row-root-b",
      "timeline-row-d-2",
      "timeline-row-decision-batch-bar",
    ]);
    // Server numbering order (created_at ASC over open cards) survives the
    // position move.
    expect(screen.getByText("decision-batch-bar:d-1,d-2")).toBeTruthy();
  });

  it("renders no batch bar for a single open card", async () => {
    mockDecisionsData.data = [decisionCard("d-1", "2026-09-05T08:00:00Z")];

    await renderTimeline([
      comment("root-a", "2026-09-05T09:00:00Z"),
      comment("root-b", "2026-09-05T10:00:00Z"),
    ]);

    expect(screen.queryByTestId("timeline-row-decision-batch-bar")).toBeNull();
    expect(screen.getByText("decision-card:d-1")).toBeTruthy();
  });
});
