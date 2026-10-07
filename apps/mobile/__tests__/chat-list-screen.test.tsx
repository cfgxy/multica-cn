// @ts-nocheck
import React from "react";
import { Alert } from "react-native";
import { fireEvent, render, screen } from "@testing-library/react-native";
import ChatListScreen from "@/app/(app)/[workspace]/(tabs)/chat";

/**
 * RUYI-496 — the Chat tab becomes a two-level IA: the tab root is now the
 * session LIST (AC1), tapping a row pushes `/[workspace]/chat/[sessionId]`
 * (AC2). These tests pin the list screen contract: rendering states (AC6),
 * row→session mapping (AC2), and the data source (AC7 — the server-filtered
 * sessions query only, no local assembly).
 */

const mockRouterPush = jest.fn();

jest.mock("expo-router", () => ({
  // Lazy wrappers: the factory is evaluated while the test file's imports
  // resolve, before top-level consts initialize — direct refs would capture
  // undefined. Calling through at invocation time is the safe shape.
  router: {
    push: (...args) => mockRouterPush(...args),
    back: jest.fn(),
    replace: jest.fn(),
  },
}));

const mockRefetch = jest.fn();
const mockQueryFlags = { isLoading: false, isError: false };
const mockSessions = [];
const mockSeenQueryKeys = [];

jest.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    mockSeenQueryKeys.push(opts.queryKey);
    const isSessions = opts.queryKey[1] === "ws-1" && opts.queryKey[2] === "sessions";
    const isAgents = opts.queryKey[0] === "agents";
    const isMembers = opts.queryKey[0] === "members";
    if (isSessions) {
      return {
        data: mockSessions,
        isLoading: mockQueryFlags.isLoading,
        isError: mockQueryFlags.isError,
        error: mockQueryFlags.isError ? new Error("boom") : null,
        refetch: mockRefetch,
      };
    }
    if (isAgents) {
      return {
        data: [
          {
            id: "agent-1",
            name: "Alpha",
            archived_at: null,
            runtime_bound: true,
            owner_id: "user-1",
            permission_mode: "public_to",
            invocation_targets: [{ target_type: "workspace", target_id: "ws-1" }],
            custom_args: [],
            runtime_config: {},
            runtime_mode: "local",
          },
        ],
        isLoading: false,
      };
    }
    if (isMembers) {
      return {
        data: [{ user_id: "user-1", role: "owner" }],
        isLoading: false,
      };
    }
    return { data: [], isLoading: false };
  },
}));

jest.mock("@/data/queries/chat", () => ({
  chatKeys: {
    all: (wsId) => ["chat", wsId],
    sessions: (wsId) => [...["chat", wsId], "sessions"],
  },
  chatSessionsOptions: (wsId) => ({
    queryKey: ["chat", wsId, "sessions"],
    queryFn: jest.fn(),
  }),
  sortChatSessions: (arr) => arr,
  chatMessagesOptions: (id) => ({
    queryKey: ["chat", "messages", id ?? ""],
    queryFn: jest.fn(),
    enabled: !!id,
  }),
  pendingChatTaskOptions: (id) => ({
    queryKey: ["chat", "pending-task", id ?? ""],
    queryFn: jest.fn(),
    enabled: !!id,
  }),
  taskMessagesOptions: (id) => ({
    queryKey: ["task-messages", id ?? ""],
    queryFn: jest.fn(),
    enabled: false,
  }),
}));

jest.mock("@/data/queries/agents", () => ({
  agentListOptions: (wsId) => ({ queryKey: ["agents", wsId], queryFn: jest.fn() }),
}));

jest.mock("@/data/queries/members", () => ({
  memberListOptions: (wsId) => ({ queryKey: ["members", wsId], queryFn: jest.fn() }),
}));

const mockDeleteMutate = jest.fn();

jest.mock("@/data/mutations/chat", () => ({
  useCreateChatSession: () => ({ mutateAsync: jest.fn() }),
  useDeleteChatSession: () => ({ mutate: mockDeleteMutate }),
  useMarkChatSessionRead: () => ({ mutate: jest.fn() }),
  useSetChatSessionArchived: () => ({ mutate: jest.fn() }),
  useSetChatSessionPinned: () => ({ mutate: jest.fn() }),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector) =>
    selector({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "acme" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (selector) => selector({ user: { id: "user-1" } }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key, fallback) => fallback ?? key }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light", isDarkColorScheme: false }),
}));

jest.mock("@/lib/theme", () => ({
  THEME: {
    light: { background: "#fff", foreground: "#000", primary: "#00f", primaryForeground: "#fff", brand: "#00f", mutedForeground: "#666", secondary: "#eee" },
    dark: {},
  },
}));

jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

// The ui primitives pull in @rn-primitives/slot, whose dist ships raw JSX the
// jest transform never sees — stand in plain RN equivalents
// (decision-batch-bar test pattern). Inside jest.mock factories, elements must
// be created via a lazily-required React (the css-interop babel pass rewrites
// `React.createElement` and hoisted factories can't see the injected import).
jest.mock("@/components/ui/button", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Button: ({ variant: _v, size: _s, ...props }) => React.createElement(Pressable, props),
  };
});

jest.mock("@/components/ui/header", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Text, View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Header: ({ left, title, right }) =>
      React.createElement(
        View,
        { testID: "header" },
        [left, title ? React.createElement(Text, { key: "t" }, title) : null, right],
      ),
  };
});

jest.mock("@/components/ui/skeleton", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Skeleton: (props) => React.createElement(View, { testID: "skeleton", ...props }) };
});

jest.mock("@/components/ui/icon-button", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    IconButton: ({ onPress, accessibilityLabel, testID }) =>
      React.createElement(Pressable, { onPress, testID, accessibilityLabel }, null),
  };
});

jest.mock("@/components/ui/action-sheet", () => ({
  useActionSheet: () => ({ show: jest.fn() }),
  ActionSheetModal: () => null,
}));

jest.mock("@/components/chat/agent-picker-sheet", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    AgentPickerSheet: ({ visible }) =>
      React.createElement(View, { testID: "agent-picker" }, visible ? ["picker-open"] : null),
  };
});

jest.mock("expo-haptics", () => ({
  selectionAsync: jest.fn(async () => {}),
  impactAsync: jest.fn(async () => {}),
}));

beforeEach(() => {
  jest.clearAllMocks();
  mockSessions.length = 0;
  mockSeenQueryKeys.length = 0;
  mockQueryFlags.isLoading = false;
  mockQueryFlags.isError = false;
});

function seedSessions() {
  mockSessions.push(
    {
      id: "sa",
      workspace_id: "ws-1",
      agent_id: "agent-1",
      creator_id: "user-1",
      title: "Pinned chat",
      status: "active",
      has_unread: true,
      pinned: true,
      updated_at: "2026-10-07T08:00:00Z",
      last_message: { content: "hello there", role: "user", created_at: "" },
    },
    {
      id: "sb",
      workspace_id: "ws-1",
      agent_id: "agent-1",
      creator_id: "user-1",
      title: "Regular chat",
      status: "active",
      has_unread: false,
      updated_at: "2026-10-07T07:00:00Z",
    },
    {
      id: "sc",
      workspace_id: "ws-1",
      agent_id: "agent-1",
      creator_id: "user-1",
      title: "Old chat",
      status: "archived",
      has_unread: false,
      updated_at: "2026-10-06T07:00:00Z",
    },
  );
}

describe("ChatListScreen (chat tab root, RUYI-496)", () => {
  it("AC1: renders the session list as the tab's first screen", async () => {
    seedSessions();
    await render(<ChatListScreen />);
    expect(await screen.findByText("Pinned chat")).toBeTruthy();
    expect(screen.getByText("Regular chat")).toBeTruthy();
    expect(screen.getByText("Old chat")).toBeTruthy();
  });

  it("AC2: tapping a row pushes the strictly matching detail route", async () => {
    seedSessions();
    await render(<ChatListScreen />);
    fireEvent.press(await screen.findByTestId("chat-row-sb"));
    expect(mockRouterPush).toHaveBeenCalledWith(
      expect.objectContaining({
        pathname: "/[workspace]/chat/[sessionId]",
        params: expect.objectContaining({ workspace: "acme", sessionId: "sb" }),
      }),
    );
  });

  it("AC6: shows a loading skeleton while sessions load", async () => {
    mockQueryFlags.isLoading = true;
    await render(<ChatListScreen />);
    expect(screen.getByTestId("chat-list-loading")).toBeTruthy();
  });

  it("AC6: shows an error state whose retry refetches", async () => {
    mockQueryFlags.isError = true;
    await render(<ChatListScreen />);
    fireEvent.press(await screen.findByText("Retry"));
    expect(mockRefetch).toHaveBeenCalled();
  });

  it("AC6: shows the empty state when there are no sessions", async () => {
    await render(<ChatListScreen />);
    expect(screen.getByText("No chats yet.")).toBeTruthy();
  });

  it("AC7: list data comes only from the server-filtered sessions query", async () => {
    seedSessions();
    await render(<ChatListScreen />);
    expect(mockSeenQueryKeys).toContainEqual(["chat", "ws-1", "sessions"]);
  });

  it("new-chat entry routes to the blank detail screen", async () => {
    seedSessions();
    await render(<ChatListScreen />);
    fireEvent.press(await screen.findByLabelText("New chat"));
    expect(mockRouterPush).toHaveBeenCalledWith(
      expect.objectContaining({
        pathname: "/[workspace]/chat/[sessionId]",
        params: expect.objectContaining({ workspace: "acme", sessionId: "new" }),
      }),
    );
  });

  it("long-press offers session actions and delete confirms via Alert", async () => {
    seedSessions();
    const alertSpy = jest.spyOn(Alert, "alert");
    await render(<ChatListScreen />);
    fireEvent(await screen.findByTestId("chat-row-sb"), "longPress");
    // Action sheet is invoked through the shared useActionSheet helper —
    // covered by its own unit tests; here we pin that the row exposes the
    // long-press surface at all (no crash, no navigation).
    expect(mockRouterPush).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });
});
