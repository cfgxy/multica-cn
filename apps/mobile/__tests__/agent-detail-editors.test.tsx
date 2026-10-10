// @ts-nocheck
import React from "react";
import { render, screen, fireEvent, waitFor } from "@testing-library/react-native";
import AgentDetailPage from "@/app/(app)/[workspace]/more/agents/[id]";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";

/**
 * RUYI-624 — the agent detail page's three editor entries are distinct:
 *
 *   - the instructions preview pushes the dedicated edit-instructions window
 *     (RUYI-541 squad pattern), never edit-profile;
 *   - the Edit Profile row pushes edit-profile;
 *   - the facts card's execution rows (runtime / model / thinking /
 *     concurrency) each push run-config; owner/status rows stay read-only;
 *   - "Assign work" seeds the quick-create smart actor with THIS agent
 *     before opening new-issue (desktop quick-create initialMode="agent"
 *     parity — the new-issue mount reset must not eat the seed; the
 *     survival itself is pinned in new-issue-assign-work.test.tsx).
 *
 * Negative assertions lock the defect: every entry used to route to
 * edit-profile, so runtime/model/concurrency had no editor and the
 * half-height profile sheet read as an instructions-only box.
 */

const LONG_TEXT =
  "Always start by writing a failing test. ".repeat(40) + "Ship small commits.";

const mockRouterPush = jest.fn();
const mockPreventRemove = jest.fn();

jest.mock("expo-router", () => ({
  router: { push: (...args) => mockRouterPush(...args), back: jest.fn() },
  useLocalSearchParams: () => ({ id: "agent-1", workspace: "ws" }),
  Stack: { Screen: ({ children }) => children },
}));

jest.mock("@react-navigation/native", () => ({
  usePreventRemove: (...args) => mockPreventRemove(...args),
  useNavigation: () => ({ dispatch: jest.fn() }),
}));

jest.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: jest.fn(async () => null),
    setItem: jest.fn(async () => undefined),
    removeItem: jest.fn(async () => undefined),
  },
}));

const mockAgent = {
  id: "agent-1",
  workspace_id: "ws-1",
  name: "Alpha",
  description: "detail agent",
  instructions: LONG_TEXT,
  avatar_url: null,
  runtime_id: "runtime-1",
  runtime_bound: true,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  model: "gpt-x",
  max_concurrent_tasks: 3,
  thinking_level: "high",
  service_tier: null,
  voice_runtime_id: null,
  session_max_context_tokens: null,
  session_compact_pct: null,
  owner_id: "user-1",
  archived_at: null,
  system_key: null,
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: "ws-1" }],
};

const mockRuntime = {
  id: "runtime-1",
  name: "Runtime One",
  custom_name: "",
  status: "online",
  provider: "claude",
};

// Flipped per test — the query mocks below read this variable.
let mockCurrentAgent = mockAgent;

jest.mock("@tanstack/react-query", () => ({
  useQuery: (opts) => {
    const key = opts.queryKey;
    if (key[0] === "agents" && key[2] === "detail") {
      return { data: mockCurrentAgent, isLoading: false, isError: false };
    }
    if (key[0] === "agents") {
      return { data: [mockCurrentAgent], isLoading: false };
    }
    if (key[0] === "members") {
      return {
        data: [{ user_id: "user-1", role: "owner", name: "Alice" }],
      };
    }
    if (key[0] === "runtimes") {
      return { data: [mockRuntime] };
    }
    if (key[0] === "runtime-models") {
      return { data: { models: [] } };
    }
    return { data: undefined, isLoading: false };
  },
  useQueries: ({ queries }) => queries.map(() => ({ data: undefined })),
}));

jest.mock("@/data/queries/agents", () => ({
  agentDetailOptions: (wsId, id) => ({
    queryKey: ["agents", wsId, "detail", id],
  }),
  agentListOptions: (wsId) => ({ queryKey: ["agents", wsId, "list"] }),
}));

jest.mock("@/data/queries/members", () => ({
  memberListOptions: (wsId) => ({ queryKey: ["members", wsId] }),
}));

jest.mock("@/data/queries/runtimes", () => ({
  runtimeListOptions: (wsId) => ({ queryKey: ["runtimes", wsId] }),
}));

jest.mock("@/data/queries/runtime-models", () => ({
  runtimeModelsOptions: (id) => ({ queryKey: ["runtime-models", id] }),
}));

jest.mock("@/data/queries/agent-task-snapshot", () => ({
  agentTaskSnapshotOptions: (wsId) => ({
    queryKey: ["agent-task-snapshot", wsId],
  }),
}));

jest.mock("@/data/queries/agent-tasks", () => ({
  agentTasksOptions: (wsId, id) => ({ queryKey: ["agent-tasks", wsId, id] }),
}));

jest.mock("@/data/queries/issues", () => ({
  issueDetailOptions: (wsId, id) => ({ queryKey: ["issues", wsId, id] }),
}));

jest.mock("@/data/queries/billing", () => ({
  appConfigOptions: () => ({ queryKey: ["app-config"] }),
}));

jest.mock("@/data/queries/integrations", () => ({
  dingTalkInstallationsOptions: () => ({ queryKey: ["integrations", "dingtalk"] }),
  larkInstallationsOptions: () => ({ queryKey: ["integrations", "lark"] }),
  slackInstallationsOptions: () => ({ queryKey: ["integrations", "slack"] }),
  telegramInstallationsOptions: () => ({ queryKey: ["integrations", "telegram"] }),
  wecomInstallationsOptions: () => ({ queryKey: ["integrations", "wecom"] }),
}));

jest.mock("@/data/api", () => ({
  ApiError: class ApiError extends Error {
    status;
    constructor(message, status) {
      super(message);
      this.status = status;
    }
  },
  api: {},
}));

jest.mock("@/data/stores/chat-agent-request-store", () => {
  const { create } = jest.requireActual("zustand");
  return {
    useChatAgentRequestStore: create(() => ({
      requestAgent: jest.fn(),
      pendingAgentId: null,
    })),
  };
});

jest.mock("@/data/server-store", () => {
  const { create } = jest.requireActual("zustand");
  return {
    useServerStore: create(() => ({ activeServerId: "server-1" })),
  };
});

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (sel) =>
    sel({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "ws" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (sel) => sel({ user: { id: "user-1" } }),
}));

jest.mock("@/data/mutations/agents", () => ({
  useArchiveAgent: () => ({ mutate: jest.fn(), isPending: false }),
  useRestoreAgent: () => ({ mutate: jest.fn(), isPending: false }),
  useCancelAgentTask: () => ({ mutate: jest.fn(), isPending: false }),
  useUpdateAgent: () => ({ mutate: jest.fn(), isPending: false }),
}));

jest.mock("@/lib/use-agent-presence", () => ({
  useWorkspacePresenceMap: () => ({ byAgent: new Map() }),
}));

jest.mock("@/lib/model-capability", () => ({
  resolveThinkingLevels: () => [{ value: "high", label: "High" }],
  findModelCapabilityEntry: () => undefined,
}));

jest.mock("@/lib/attachment-url", () => ({
  resolveAttachmentUrl: (url) => url,
}));

jest.mock("@/components/agents/access-scope-badge", () => ({
  AccessScopeBadge: () => null,
}));

jest.mock("@/components/agents/agent-presence-line", () => ({
  AgentPresenceLine: () => null,
}));

jest.mock("@/components/agents/swipeable-agent-task-row", () => ({
  SwipeableAgentTaskRow: () => null,
}));

jest.mock("@/components/agents/agent-run-history-row", () => ({
  AgentRunHistoryRow: () => null,
}));

jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

jest.mock("@/components/ui/action-sheet", () => ({
  ActionSheetModal: () => null,
  useActionSheet: () => ({ modalProps: {}, show: jest.fn() }),
}));

jest.mock("@/components/ui/tabs", () => {
  const React = jest.requireActual("react");
  const { View } = jest.requireActual("react-native");
  return {
    Tabs: ({ children }) => React.createElement(View, null, children),
    TabsList: ({ children }) => React.createElement(View, null, children),
    TabsTrigger: ({ children }) => React.createElement(View, null, children),
    TabsContent: ({ children }) => React.createElement(View, null, children),
  };
});

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key, fallback) => fallback ?? key }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@/lib/theme", () => ({
  THEME: {
    light: { foreground: "#000", mutedForeground: "#666", warning: "#a00" },
    dark: { foreground: "#fff", mutedForeground: "#999", warning: "#f00" },
  },
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

jest.mock("expo-haptics", () => ({
  impactAsync: jest.fn().mockResolvedValue(undefined),
  notificationAsync: jest.fn().mockResolvedValue(undefined),
  ImpactFeedbackStyle: { Medium: "medium" },
  NotificationFeedbackType: { Success: "success" },
}));

function expectPushed(pathname) {
  expect(mockRouterPush).toHaveBeenCalledWith({
    pathname,
    params: { workspace: "ws", id: "agent-1" },
  });
}

beforeEach(() => {
  jest.clearAllMocks();
  mockCurrentAgent = mockAgent;
  useNewIssueDraftStore.getState().reset();
});

describe("AgentDetailPage editor entries (RUYI-624)", () => {
  it("instructions preview pushes the dedicated edit-instructions window, never edit-profile", async () => {
    await render(<AgentDetailPage />);
    await fireEvent.press(screen.getByLabelText("Edit instructions"));
    expectPushed("/[workspace]/more/agents/[id]/edit-instructions");
    const pushedPaths = mockRouterPush.mock.calls.map(
      (c) => c[0]?.pathname ?? c[0],
    );
    expect(pushedPaths).not.toContain("/[workspace]/more/agents/[id]/edit-profile");
  });

  it("Edit Profile row pushes edit-profile", async () => {
    await render(<AgentDetailPage />);
    await fireEvent.press(screen.getByLabelText("Edit Profile"));
    expectPushed("/[workspace]/more/agents/[id]/edit-profile");
  });

  it.each([
    ["Runtime", "Runtime — Execution"],
    ["Model", "Model — Execution"],
    ["Concurrency cap", "Concurrency cap — Execution"],
    ["Thinking", "Thinking — Execution"],
  ])("%s facts row pushes run-config", async (_label, accessibility) => {
    await render(<AgentDetailPage />);
    await fireEvent.press(screen.getByLabelText(accessibility));
    expectPushed("/[workspace]/more/agents/[id]/run-config");
  });

  it("owner and status rows are read-only (no Execution tap-through)", async () => {
    await render(<AgentDetailPage />);
    expect(screen.queryByLabelText("Owner — Execution")).toBeNull();
    expect(screen.queryByLabelText("Status — Execution")).toBeNull();
    expect(screen.getByText("Alice")).toBeTruthy();
  });

  it("Assign work seeds the quick-create smart actor with this agent and opens new-issue", async () => {
    await render(<AgentDetailPage />);
    await fireEvent.press(screen.getByLabelText("Assign work"));
    await waitFor(() => {
      expect(useNewIssueDraftStore.getState().smartActor).toEqual({
        type: "agent",
        id: "agent-1",
      });
    });
    expect(mockRouterPush).toHaveBeenCalledWith("/ws/new-issue");
  });

  it("archived agent: execution rows lose their tap-through and the preview is inert", async () => {
    mockCurrentAgent = { ...mockAgent, archived_at: "2026-10-01T00:00:00Z" };
    await render(<AgentDetailPage />);
    expect(screen.queryByLabelText("Runtime — Execution")).toBeNull();
    expect(screen.queryByLabelText("Edit Profile")).toBeNull();
    // Preview renders without a pressable wrapper (onTap undefined).
    expect(screen.queryByLabelText("Edit instructions")).toBeNull();
    expect(screen.getByText(LONG_TEXT)).toBeTruthy();
    expect(mockRouterPush).not.toHaveBeenCalled();
  });
});
