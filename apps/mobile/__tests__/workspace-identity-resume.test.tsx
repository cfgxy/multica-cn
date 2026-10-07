// @ts-nocheck
import React from "react";
import { Text, View } from "react-native";
import { act, render, screen } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import InboxScreen from "@/app/(app)/[workspace]/(tabs)/inbox";
import { useWorkspaceStore } from "@/data/workspace-store";

/**
 * RUYI-543 — 切后台再切回后所有列表显示为空（像选错了空间）。
 *
 * 四个 tab 的查询 key 与实时订阅全部挂在 workspace-store 的
 * currentWorkspaceId 上。mid-session 的 restoreSlug()（authStore.initialize()
 * 的冷启动恢复步骤）曾把 id 无条件置 null：id 一翻 null，全部列表查询的
 * key 瞬间变成 `[..., null, ...]` 且 enabled:false —— 数据未加载、无错误、
 * 无 spinner，每页直接落空态，而布局 effect 的依赖不变、不会自愈。
 * 冷启动路径有「layout effect → matched → setCurrentWorkspace」完整修复链
 * 所以无恙；mid-session 重放没有，现象即 Owner 所见「像选错了空间」。
 *
 * 这些测试钉住恢复语义：**持久化 slug 未变时，restoreSlug 不得清掉已确认的
 * currentWorkspaceId**（mid-session 重入安全）；持久化 slug 变化时（切服
 * 务器）维持既有「连 id 一起清」语义，防止旧服务器的工作区 id 泄漏。
 */

const mockRouterPush = jest.fn();

jest.mock("expo-router", () => ({
  router: {
    push: (...args) => mockRouterPush(...args),
    back: jest.fn(),
    replace: jest.fn(),
  },
}));

const mockListInbox = jest.fn();

jest.mock("@/data/api", () => ({
  api: { listInbox: (...args) => mockListInbox(...args) },
}));

jest.mock("@/data/queries/agent-task-snapshot", () => ({
  agentTaskSnapshotOptions: (wsId) => ({
    queryKey: ["agent-task-snapshot", wsId],
    queryFn: () => Promise.resolve([]),
    enabled: !!wsId,
  }),
}));

const mockMutationResult = () => ({
  mutate: jest.fn(),
  mutateAsync: jest.fn(),
  isPending: false,
});

jest.mock("@/data/mutations/inbox", () => ({
  useArchiveAllInbox: () => mockMutationResult(),
  useArchiveAllReadInbox: () => mockMutationResult(),
  useArchiveCompletedInbox: () => mockMutationResult(),
  useArchiveInbox: () => mockMutationResult(),
  useMarkAllInboxRead: () => mockMutationResult(),
  useMarkInboxRead: () => mockMutationResult(),
}));

// workspace-store 依赖的两条 IO 边界：SecureStore 槽位 slug 表与 server-store。
// 内存版 slug 表让每个用例自己摆「持久化 slug 是否与内存一致」。
const mockSecureSlugs = new Map<string, string | null>();

jest.mock("@/data/secure-storage", () => ({
  getSlug: (serverId) =>
    Promise.resolve(mockSecureSlugs.get(serverId) ?? null),
  setSlug: (serverId, slug) => {
    mockSecureSlugs.set(serverId, slug);
    return Promise.resolve();
  },
  clearSlug: (serverId) => {
    mockSecureSlugs.delete(serverId);
    return Promise.resolve();
  },
}));

jest.mock("@/data/server-store", () => ({
  useServerStore: {
    getState: () => ({ activeServerId: "server-1" }),
  },
}));

jest.mock("@/data/stores/new-issue-draft-store", () => ({
  invalidateNewIssueSubmissionContext: jest.fn(),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key, fallback) => fallback ?? key }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light", isDarkColorScheme: false }),
}));

jest.mock("@/lib/theme", () => ({
  THEME: {
    light: { background: "#fff", foreground: "#000", primary: "#00f", mutedForeground: "#666" },
    dark: {},
  },
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

jest.mock("@/components/ui/text", () => {
  const React = jest.requireActual("react");
  const { Text: RNText } = jest.requireActual("react-native");
  return {
    Text: (props) => React.createElement(RNText, props),
  };
});

jest.mock("@/components/ui/button", () => {
  const React = jest.requireActual("react");
  const { Pressable } = jest.requireActual("react-native");
  return {
    Button: ({ variant: _v, size: _s, ...props }) =>
      React.createElement(Pressable, props),
  };
});

jest.mock("@/components/ui/header", () => {
  const React = jest.requireActual("react");
  const { Text: RNText, View: RNView } = jest.requireActual("react-native");
  return {
    Header: ({ title }) =>
      React.createElement(
        RNView,
        { testID: "header" },
        title ? React.createElement(RNText, null, title) : null,
      ),
  };
});

jest.mock("@/components/ui/app-header-actions", () => ({
  HeaderActions: () => null,
}));

jest.mock("@/components/ui/icon-button", () => {
  const React = jest.requireActual("react");
  const { Pressable } = jest.requireActual("react-native");
  return {
    IconButton: ({ onPress, accessibilityLabel }) =>
      React.createElement(Pressable, { onPress, accessibilityLabel }, null),
  };
});

jest.mock("@/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }) => null,
  DropdownMenuTrigger: ({ children }) => null,
  DropdownMenuContent: ({ children }) => null,
  DropdownMenuItem: ({ children }) => null,
  DropdownMenuSeparator: () => null,
}));

jest.mock("@/components/ui/skeleton", () => {
  const React = jest.requireActual("react");
  const { View: RNView } = jest.requireActual("react-native");
  return { Skeleton: (props) => React.createElement(RNView, props) };
});

jest.mock("@/components/inbox/swipeable-inbox-row", () => {
  const React = jest.requireActual("react");
  const { Text: RNText, View: RNView } = jest.requireActual("react-native");
  return {
    SwipeableInboxRow: ({ item }) =>
      React.createElement(
        RNView,
        { testID: `inbox-row-${item.id}` },
        React.createElement(RNText, null, item.id),
      ),
  };
});

const ITEMS = [
  { id: "inbox-1", read: false, archived: false, issue_id: null, created_at: "2026-10-08T00:00:00Z", details: null },
  { id: "inbox-2", read: false, archived: false, issue_id: null, created_at: "2026-10-08T00:01:00Z", details: null },
];

async function seedWorkspaceIdentity() {
  mockSecureSlugs.set("server-1", "repro543");
  await act(async () => {
    await useWorkspaceStore.getState().setCurrentWorkspace("ws-uuid-1", "repro543");
  });
}

function renderInbox() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  mockListInbox.mockResolvedValue(ITEMS);
  render(
    <QueryClientProvider client={qc}>
      <InboxScreen />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => {
  jest.clearAllMocks();
  mockSecureSlugs.clear();
  useWorkspaceStore.setState({
    currentWorkspaceId: null,
    currentWorkspaceSlug: null,
  });
});

it("初始加载后收件箱渲染两条数据", async () => {
  renderInbox();
  expect(await screen.findByTestId("inbox-row-inbox-1")).toBeTruthy();
  expect(screen.getByTestId("inbox-row-inbox-2")).toBeTruthy();
});

it("mid-session 恢复（持久化 slug 未变）不得清掉已确认的工作区 id——列表保持可见", async () => {
  renderInbox();
  expect(await screen.findByTestId("inbox-row-inbox-1")).toBeTruthy();

  await seedWorkspaceIdentity();
  await act(async () => {
    await useWorkspaceStore.getState().restoreSlug();
  });

  expect(useWorkspaceStore.getState().currentWorkspaceId).toBe("ws-uuid-1");
  expect(useWorkspaceStore.getState().currentWorkspaceSlug).toBe("repro543");
  expect(screen.getByTestId("inbox-row-inbox-1")).toBeTruthy();
  expect(screen.getByTestId("inbox-row-inbox-2")).toBeTruthy();
  expect(screen.queryByText("No notifications")).toBeNull();
});

it("持久化 slug 变化（切服务器）时维持既有语义：id 与 slug 一起切换为新值", async () => {
  renderInbox();
  expect(await screen.findByTestId("inbox-row-inbox-1")).toBeTruthy();

  await seedWorkspaceIdentity();
  mockSecureSlugs.set("server-1", "other-ws");
  await act(async () => {
    await useWorkspaceStore.getState().restoreSlug();
  });

  expect(useWorkspaceStore.getState().currentWorkspaceId).toBeNull();
  expect(useWorkspaceStore.getState().currentWorkspaceSlug).toBe("other-ws");
});
