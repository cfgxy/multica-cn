// @ts-nocheck
import React from "react";
import { Alert } from "react-native";
import { fireEvent, render, screen } from "@testing-library/react-native";
import ChatDetailScreen from "@/app/(app)/[workspace]/chat/[sessionId]";
import { useChatAgentRequestStore } from "@/data/stores/chat-agent-request-store";

/**
 * RUYI-496 — the chat screen (messages / composer / agent session) moves
 * off the tab root into `/[workspace]/chat/[sessionId]`. These tests pin
 * the detail screen contract: per-route session isolation (AC4), the
 * delete→back-to-list link (AC5), the "new" blank-chat mode (no message
 * query, agent-request handoff), and read marking.
 */

const mockRouterBack = jest.fn();
const mockRouterPush = jest.fn();
const mockRoute = { sessionId: "sa", agentId: undefined };

jest.mock("expo-router", () => ({
  // Lazy wrappers: the factory is evaluated while the test file's imports
  // resolve, before top-level consts initialize — direct refs would capture
  // undefined. Calling through at invocation time is the safe shape.
  useLocalSearchParams: () => mockRoute,
  router: {
    back: (...args) => mockRouterBack(...args),
    push: (...args) => mockRouterPush(...args),
    replace: jest.fn(),
  },
}));

const mockFocused = true;

jest.mock("@react-navigation/native", () => ({
  useIsFocused: () => mockFocused,
  useFocusEffect: (callback: () => void | (() => void)) => {
    const React = jest.requireActual<typeof import("react")>("react");
    React.useEffect(() => callback(), [callback]);
  },
}));

const mockMessagesBySession = {};
const mockSessions = [];
const mockSeenMessageOptionIds = [];
const mockMarkReadMutate = jest.fn();
const mockDeleteMutate = jest.fn();
const mockCreateMutateAsync = jest.fn();

jest.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    const key = JSON.stringify(opts.queryKey);
    if (key.includes('"sessions"')) {
      return { data: mockSessions, isLoading: false, isError: false };
    }
    if (key.includes('"messages"')) {
      const sessionId = opts.queryKey[2];
      if (!sessionId) return { data: [], isLoading: false };
      return { data: mockMessagesBySession[sessionId] ?? [], isLoading: false };
    }
    if (key.includes('"pending-task"') || key.includes("task-messages")) {
      return { data: null, isLoading: false };
    }
    if (opts.queryKey[0] === "agents") {
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
          {
            id: "agent-2",
            name: "Beta",
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
    if (opts.queryKey[0] === "members") {
      return { data: [{ user_id: "user-1", role: "owner" }], isLoading: false };
    }
    return { data: [], isLoading: false };
  },
  useQueryClient: () => ({
    setQueryData: jest.fn(),
    invalidateQueries: jest.fn(),
    cancelQueries: jest.fn(),
  }),
}));

jest.mock("@/data/queries/chat", () => ({
  chatKeys: {
    messages: (id) => ["chat", "messages", id],
    pendingTask: (id) => ["chat", "pending-task", id],
  },
  chatSessionsOptions: (wsId) => ({
    queryKey: ["chat", wsId, "sessions"],
    queryFn: jest.fn(),
  }),
  chatMessagesOptions: (id) => {
    mockSeenMessageOptionIds.push(id);
    return {
      queryKey: ["chat", "messages", id ?? ""],
      queryFn: jest.fn(),
      enabled: !!id,
    };
  },
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

jest.mock("@/data/mutations/chat", () => ({
  useCreateChatSession: () => ({ mutateAsync: mockCreateMutateAsync }),
  useDeleteChatSession: () => ({ mutate: mockDeleteMutate }),
  useMarkChatSessionRead: () => ({ mutate: mockMarkReadMutate }),
  useSetChatSessionArchived: () => ({ mutate: jest.fn() }),
  useSetChatSessionPinned: () => ({ mutate: jest.fn() }),
}));

jest.mock("@/data/stores/chat-drafts-store", () => ({
  DRAFT_NEW_SESSION: "__new__",
  useChatDraftsStore: (selector) =>
    selector({
      drafts: {},
      setDraft: jest.fn(),
      clearDraft: jest.fn(),
      promoteNewDraft: jest.fn(),
    }),
}));

jest.mock("@/data/stores/shared-intent-store", () => ({
  useSharedIntentStore: { getState: () => ({ takeFor: () => null }) },
}));

jest.mock("@/data/chat-select-store", () => ({
  useChatSelectStore: { getState: () => ({ clear: jest.fn() }) },
}));

const mockOnDeleted = jest.fn();

jest.mock("@/data/realtime/use-chat-session-realtime", () => ({
  useChatSessionRealtime: (sessionId, onDeleted) => {
    mockOnDeleted.mockImplementation(onDeleted);
  },
}));

jest.mock("@/data/realtime/chat-ws-updaters", () => ({
  invalidatePendingTask: jest.fn(),
  seedAcceptedPendingTask: jest.fn(),
}));

jest.mock("@/lib/workspace-agent-availability", () => ({
  useWorkspaceAgentAvailability: () => "full",
}));

jest.mock("@/lib/use-agent-presence", () => ({
  useAgentPresence: () => ({ availability: "online" }),
}));

jest.mock("@/lib/is-agent-runtime-bound", () => ({
  isAgentRuntimeBound: () => true,
}));

jest.mock("@/lib/dispatch-reason", () => ({
  sendFailureMessage: () => "send failed",
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

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector) =>
    selector({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "acme" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (selector) => selector({ user: { id: "user-1" } }),
}));

jest.mock("@/data/api", () => ({
  api: { sendChatMessage: jest.fn(async () => ({})) },
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("react-native-keyboard-controller", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    KeyboardAvoidingView: ({ children }) => React.createElement(View, null, children),
  };
});

jest.mock("@/components/chat/chat-message-list", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View, Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    ChatMessageList: ({ messages }) =>
      React.createElement(
        View,
        { testID: "chat-message-list" },
        messages.map((m) =>
          React.createElement(Text, { key: m.id }, `msg:${m.content}`),
        ),
      ),
  };
});

jest.mock("@/components/chat/chat-composer", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Text, View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    ChatComposer: ({ disabled, disabledReason }) =>
      React.createElement(
        View,
        { testID: "chat-composer", disabled: String(disabled) },
        disabledReason ? [React.createElement(Text, { key: "r" }, disabledReason)] : null,
      ),
  };
});

jest.mock("@/components/chat/agent-picker-sheet", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return { AgentPickerSheet: ({ visible }) => React.createElement(View, { testID: "agent-picker" }, visible ? ["picker-open"] : null) };
});

jest.mock("@/components/voice/voice-session-overlay", () => ({
  VoiceSessionOverlay: () => null,
}));

jest.mock("@/components/chat/no-agent-banner", () => ({
  NoAgentBanner: () => null,
}));

jest.mock("@/components/chat/offline-banner", () => ({
  OfflineBanner: () => null,
}));

jest.mock("@/components/chat/runtime-required-banner", () => ({
  RuntimeRequiredBanner: () => null,
}));

jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

jest.mock("@/components/ui/icon-button", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    IconButton: ({ onPress, accessibilityLabel, testID }) =>
      React.createElement(Pressable, { onPress, testID, accessibilityLabel }, null),
  };
});

// The ui primitives pull in @rn-primitives/slot, whose dist ships raw JSX the
// jest transform never sees — stand in plain RN equivalents
// (decision-batch-bar test pattern). Inside jest.mock factories, elements must
// be created via a lazily-required React (the css-interop babel pass rewrites
// `React.createElement` and hoisted factories can't see the injected import).
jest.mock("@/components/ui/header", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Header: ({ left, center, right }) =>
      React.createElement(View, { testID: "header" }, [left, center, right]),
  };
});

jest.mock("@/components/ui/dropdown-menu", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable, View } = jest.requireActual<typeof import("react-native")>("react-native");
  const Item = ({ onPress, children }) =>
    React.createElement(Pressable, { onPress, testID: "menu-item" }, children);
  return {
    DropdownMenu: ({ children }) => React.createElement(View, null, children),
    DropdownMenuTrigger: ({ children }) => React.createElement(View, null, children),
    DropdownMenuContent: ({ children }) => React.createElement(View, null, children),
    DropdownMenuItem: Item,
    DropdownMenuSeparator: () => null,
  };
});

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

function seedWorld() {
  mockSessions.length = 0;
  mockSessions.push(
    {
      id: "sa",
      workspace_id: "ws-1",
      agent_id: "agent-1",
      creator_id: "user-1",
      title: "Session A",
      status: "active",
      has_unread: true,
      updated_at: "2026-10-07T08:00:00Z",
    },
    {
      id: "sb",
      workspace_id: "ws-1",
      agent_id: "agent-2",
      creator_id: "user-1",
      title: "Session B",
      status: "active",
      has_unread: false,
      updated_at: "2026-10-07T07:00:00Z",
    },
  );
  mockMessagesBySession.sa = [
    { id: "m1", chat_session_id: "sa", role: "user", content: "hello A", task_id: null, created_at: "" },
  ];
  mockMessagesBySession.sb = [
    { id: "m2", chat_session_id: "sb", role: "assistant", content: "hello B", task_id: null, created_at: "" },
  ];
}

beforeEach(() => {
  jest.clearAllMocks();
  mockRoute.sessionId = "sa";
  mockRoute.agentId = undefined;
  mockSeenMessageOptionIds.length = 0;
  seedWorld();
  useChatAgentRequestStore.getState().consumeAgent();
});

describe("ChatDetailScreen (RUYI-496)", () => {
  it("AC4: queries and renders messages of the routed session only", async () => {
    await render(<ChatDetailScreen />);
    expect(mockSeenMessageOptionIds).toContain("sa");
    expect(await screen.findByText("msg:hello A")).toBeTruthy();
    expect(screen.queryByText("msg:hello B")).toBeNull();
  });

  it("AC4: a different route param isolates the session's state", async () => {
    mockRoute.sessionId = "sb";
    await render(<ChatDetailScreen />);
    expect(mockSeenMessageOptionIds).toContain("sb");
    expect(await screen.findByText("msg:hello B")).toBeTruthy();
    expect(screen.queryByText("msg:hello A")).toBeNull();
  });

  it("AC4: the new-chat param runs no message query and creates nothing", async () => {
    mockRoute.sessionId = "new";
    await render(<ChatDetailScreen />);
    expect(mockSeenMessageOptionIds).toContain(null);
    expect(mockCreateMutateAsync).not.toHaveBeenCalled();
    expect(screen.getByTestId("chat-composer")).toBeTruthy();
  });

  it("RUYI-418 link: a pending agent request selects that agent in new chat", async () => {
    mockRoute.sessionId = "new";
    useChatAgentRequestStore.getState().requestAgent("agent-2");
    await render(<ChatDetailScreen />);
    expect(screen.getByText("Beta")).toBeTruthy();
    expect(useChatAgentRequestStore.getState().agentRequest).toBeNull();
  });

  it("AC5: deleting the active session mutates and routes back to the list", async () => {
    const alertSpy = jest.spyOn(Alert, "alert");
    await render(<ChatDetailScreen />);
    // Open the header ⋯ menu and hit "Delete chat".
    fireEvent.press(await screen.findByLabelText("Session actions"));
    fireEvent.press(screen.getByText("Delete chat"));
    const destructive = alertSpy.mock.calls.at(-1);
    const confirmButton = destructive[2].find((b) => b.style === "destructive");
    confirmButton.onPress();
    expect(mockDeleteMutate).toHaveBeenCalledWith("sa");
    expect(mockRouterBack).toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("AC5: session-deleted realtime callback routes back to the list", async () => {
    await render(<ChatDetailScreen />);
    mockOnDeleted();
    expect(mockRouterBack).toHaveBeenCalled();
  });

  it("marks an unread session read while focused", async () => {
    await render(<ChatDetailScreen />);
    expect(mockMarkReadMutate).toHaveBeenCalledWith("sa");
  });

  it("new chat honors the preselected agent param", async () => {
    mockRoute.sessionId = "new";
    mockRoute.agentId = "agent-2";
    await render(<ChatDetailScreen />);
    expect(screen.getByText("Beta")).toBeTruthy();
  });
});
