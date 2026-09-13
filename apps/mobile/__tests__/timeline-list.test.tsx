// @ts-nocheck

import React, { useSyncExternalStore } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react-native";
import { Pressable, Text, View } from "react-native";
import type { Issue, TimelineEntry } from "@multica/core/types";
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

jest.mock("expo-haptics", () => ({
  impactAsync: jest.fn(),
}));

jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [] }),
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
jest.mock("@/data/comment-select-store", () => ({
  useCommentSelectStore: Object.assign(
    (selector: (state: { selectingId: null }) => unknown) => selector({ selectingId: null }),
    { getState: () => ({ clear: jest.fn() }) },
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
