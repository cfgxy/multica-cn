// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * RUYI-623 — Tasks tab 的 members/agents/squads 三查询 enabled 收敛。
 * 这三个列表在首页只服务 assignee/creator 筛选 chip 的名字解析（行、
 * 头部与其它 tab 的渲染各自有自己的数据源），因此无 actor 筛选时必须
 * 不 enabled——首页返回热路径少三次后台取数；有 actor 筛选时恢复。
 *
 * 手法同 workspace-layout.test.ts：mock 全部依赖后直接调用 Tasks
 * 组件，捕获 useQuery 收到的 options 断言 enabled。
 */

const state = vi.hoisted(() => ({
  queries: [] as Array<Record<string, unknown>>,
  view: {
    tab: "all",
    sortBy: "updated_at",
    statusFilters: [] as string[],
    priorityFilters: [] as string[],
    mineRelations: {
      assigned: false,
      created: false,
      involved: false,
    } as Record<string, boolean>,
    assigneeRefs: [] as Array<{ type: string; id: string }>,
    includeNoAssignee: false,
    creatorRefs: [] as Array<{ type: string; id: string }>,
    agentRunning: false,
  },
}));

vi.mock("react", () => ({
  useEffect: vi.fn(),
  useMemo: (fn: () => unknown) => fn(),
}));
vi.mock("react-native", () => ({
  Platform: { OS: "ios" },
  Pressable: () => null,
  SectionList: () => null,
  ScrollView: () => null,
  View: () => null,
  useWindowDimensions: () => ({ fontScale: 1 }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: Record<string, unknown>) => {
    state.queries.push(options);
    return {
      data: undefined,
      isLoading: false,
      error: null,
      isRefetching: false,
      refetch: vi.fn(),
    };
  },
}));
vi.mock("@react-navigation/native", () => ({ useIsFocused: () => true }));
vi.mock("expo-router", () => ({ router: { push: vi.fn() } }));
vi.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
vi.mock("@/components/ui/text", () => ({ Text: () => null }));
vi.mock("@/components/ui/button", () => ({ Button: () => null }));
vi.mock("@/components/ui/header", () => ({ Header: () => null }));
vi.mock("@/components/ui/app-header-actions", () => ({ HeaderActions: () => null }));
vi.mock("@/components/ui/status-icon", () => ({ StatusIcon: () => null }));
vi.mock("@/components/issue/issue-row-inbox", () => ({ IssueRowInbox: () => null }));
vi.mock("@/components/issue/issues-loading", () => ({ IssuesLoading: () => null }));
vi.mock("@/data/queries/tasks", () => ({
  buildTaskListFilter: (args: Record<string, unknown>) => args,
  mergeTaskIssues: () => [],
  runningIssueIdsFromSnapshot: () => [],
  taskListOptions: (wsId: string | null, filter: unknown) => ({
    queryKey: ["task-list", wsId, filter],
    queryFn: () => [],
  }),
  TASK_TAB_CATEGORIES: {
    all: ["todo"],
    open: ["todo"],
    active: ["in_progress"],
    blocked: ["blocked"],
    completed: ["done"],
  },
}));
vi.mock("@/data/queries/agent-task-snapshot", () => ({
  agentTaskSnapshotOptions: (wsId: string | null) => ({
    queryKey: ["agent-task-snapshot", wsId],
    queryFn: () => [],
  }),
}));
vi.mock("@/data/queries/members", () => ({
  memberListOptions: (wsId: string | null) => ({
    queryKey: ["members", wsId],
    queryFn: () => [],
    enabled: !!wsId,
  }),
}));
vi.mock("@/data/queries/agents", () => ({
  agentListOptions: (wsId: string | null) => ({
    queryKey: ["agents", wsId, "list"],
    queryFn: () => [],
    enabled: !!wsId,
  }),
}));
vi.mock("@/data/queries/squads", () => ({
  squadListOptions: (wsId: string | null) => ({
    queryKey: ["squads", wsId, "list"],
    queryFn: () => [],
    enabled: !!wsId,
  }),
}));
vi.mock("@/data/stores/tasks-view-store", () => ({
  useTasksViewStore: (selector: (s: typeof state.view) => unknown) =>
    selector(state.view),
}));
vi.mock("@/data/auth-store", () => ({
  useAuthStore: (
    selector: (s: { user: { id: string } | null }) => unknown,
  ) => selector({ user: { id: "user-1" } }),
}));
vi.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (
    selector: (s: { currentWorkspaceId: string; currentWorkspaceSlug: string }) => unknown,
  ) => selector({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "acme" }),
}));
vi.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));
vi.mock("@/lib/use-issue-statuses", () => ({
  useIssueStatuses: () => ({ categoryOf: () => "todo" }),
}));
vi.mock("@/lib/issue-status", () => ({
  localizedStatusLabel: () => "",
  priorityLabel: () => "",
  statusLabel: () => "",
  statusCategoryOfKey: () => "todo",
  issueColumnCategory: () => "todo",
  BOARD_CATEGORIES: [],
}));
vi.mock("@/lib/theme", () => ({
  THEME: {
    light: { brand: "#000000", mutedForeground: "#666666" },
    dark: { brand: "#ffffff", mutedForeground: "#999999" },
  },
}));
vi.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, defaultValue?: string) => defaultValue ?? _key }),
}));

import Tasks from "../app/(app)/[workspace]/(tabs)/tasks";

function enabledOf(queryKeyHead: string): unknown {
  const entry = state.queries.find(
    (q) => Array.isArray(q.queryKey) && q.queryKey[0] === queryKeyHead,
  );
  if (!entry) throw new Error(`query ${queryKeyHead} 未挂载`);
  return entry.enabled;
}

describe("Tasks tab 三查询 enabled 收敛（RUYI-623）", () => {
  beforeEach(() => {
    state.queries.length = 0;
    state.view.assigneeRefs = [];
    state.view.creatorRefs = [];
  });

  it("无 actor 筛选：三查询 enabled=false，不进首页热路径；列表与快照查询不受影响", () => {
    Tasks();
    expect(enabledOf("members")).toBe(false);
    expect(enabledOf("agents")).toBe(false);
    expect(enabledOf("squads")).toBe(false);
    expect(enabledOf("agent-task-snapshot")).toBe(true);
    expect(enabledOf("task-list")).toBe(true);
  });

  it("有 assignee 筛选：三查询恢复 enabled（chip 需要名字解析）", () => {
    state.view.assigneeRefs = [{ type: "member", id: "u-1" }];
    Tasks();
    expect(enabledOf("members")).toBe(true);
    expect(enabledOf("agents")).toBe(true);
    expect(enabledOf("squads")).toBe(true);
  });

  it("有 creator 筛选：同样恢复 enabled", () => {
    state.view.creatorRefs = [{ type: "agent", id: "a-1" }];
    Tasks();
    expect(enabledOf("members")).toBe(true);
    expect(enabledOf("agents")).toBe(true);
    expect(enabledOf("squads")).toBe(true);
  });
});
