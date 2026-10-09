// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * RUYI-623 — workspace 布局的嵌套 picker 接线：modal（new-issue /
 * project/new）之上叠 formSheet 的草稿 picker 路由在 Android 必须以全屏
 * modal 呈现（上游嵌套 formSheet dismiss 失同步 #4331 / #4090，同
 * runs/[taskId] 先例），单层 formSheet 在双平台保持既有 sheet 路径，
 * iOS 全部保持 sheet chrome。
 *
 * 手法同 workspace-layout.test.ts：mock 全部依赖后直接调用布局组件。
 * 布局未经 React 渲染器执行，`<Stack.Screen>` 只会以元素对象（type 恒等
 * 于 expo-router 的 Stack.Screen mock）出现在返回树里——遍历该树收集
 * name/options 断言，不依赖组件函数被调用。
 */

const state = vi.hoisted(() => ({
  platformOS: "android",
  captured: [] as Array<{ name: string; options: Record<string, unknown> }>,
  useQuery: vi.fn(),
  redirect: vi.fn(),
  setCurrentWorkspace: vi.fn(),
}));

vi.mock("react", () => ({ useEffect: vi.fn() }));
vi.mock("react-native", () => ({
  Platform: {
    get OS() {
      return state.platformOS;
    },
  },
}));
vi.mock("expo-router", () => ({
  Redirect: state.redirect,
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({ workspace: "acme" }),
}));
vi.mock("@react-navigation/native", () => ({ useIsFocused: () => true }));
vi.mock("@tanstack/react-query", () => ({ useQuery: state.useQuery }));
vi.mock("i18next", () => ({
  default: { t: (_key: string, defaultValue?: string) => defaultValue },
}));
vi.mock("@/data/auth-store", () => ({
  useAuthStore: (selector: (s: { isServerSwitching: boolean }) => unknown) =>
    selector({ isServerSwitching: false }),
}));
vi.mock("@/data/queries/workspaces", () => ({
  workspaceListOptions: () => ({ queryKey: ["workspaces"], queryFn: vi.fn() }),
}));
vi.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (
    selector: (s: { setCurrentWorkspace: typeof state.setCurrentWorkspace }) => unknown,
  ) => selector({ setCurrentWorkspace: state.setCurrentWorkspace }),
}));
vi.mock("@/data/realtime/realtime-provider", () => ({ RealtimeProvider: () => null }));
vi.mock("@/data/realtime/use-inbox-realtime", () => ({ useInboxRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-notification-realtime", () => ({ useNotificationRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-issues-realtime", () => ({ useIssuesRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-my-issues-realtime", () => ({ useMyIssuesRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-chat-sessions-realtime", () => ({ useChatSessionsRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-projects-realtime", () => ({ useProjectsRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-pins-realtime", () => ({ usePinsRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-squads-realtime", () => ({ useSquadsRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-presence-realtime", () => ({ usePresenceRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-decision-inbox-realtime", () => ({
  useDecisionInboxRealtime: vi.fn(),
}));
vi.mock("@/lib/use-workspace-presence-prefetch", () => ({
  useWorkspacePresencePrefetch: vi.fn(),
}));
vi.mock("@/components/ui/modal-close-button", () => ({ ModalCloseButton: () => null }));
vi.mock("@/data/stores/new-issue-draft-store", () => ({
  useNewIssueDraftResetOnWorkspaceChange: vi.fn(),
}));
vi.mock("@/data/stores/new-project-draft-store", () => ({
  useNewProjectDraftResetOnWorkspaceChange: vi.fn(),
}));
vi.mock("@/data/stores/chat-agent-request-store", () => ({
  useChatAgentRequestResetOnWorkspaceChange: vi.fn(),
}));

import { Stack } from "expo-router";

import WorkspaceLayout from "../app/(app)/[workspace]/_layout";

/** modal 之上叠 formSheet 的全部路由（本单收敛对象）。 */
const NESTED_PICKERS = [
  "new-issue-picker/status",
  "new-issue-picker/priority",
  "new-issue-picker/assignee",
  "new-issue-picker/project",
  "new-issue-picker/due-date",
  "new-issue-picker/actor",
  "new-project-picker/status",
  "new-project-picker/priority",
];

/** 单层 formSheet（父级是普通压栈屏/tabs）——必须保持不动。 */
const SINGLE_LAYER_SHEETS = [
  "issue/[id]/picker/status",
  "issue/[id]/picker/assignee",
  "issue/[id]/runs",
  "inbox/[id]",
  "issues-filter",
  "tasks-sort",
  "tasks-actor-picker",
  "decisions-filter",
  "switch-workspace",
  "chat-rename",
  "more/agents/[id]/skills",
  "more/squads/[id]/add-member",
];

interface ElementLike {
  type?: unknown;
  props?: {
    children?: unknown;
    name?: string;
    options?: Record<string, unknown>;
  };
}

function collectScreens(node: unknown): void {
  if (!node || typeof node !== "object") return;
  if (Array.isArray(node)) {
    for (const child of node) collectScreens(child);
    return;
  }
  const element = node as ElementLike;
  if (element.type === Stack.Screen) {
    state.captured.push({ name: element.props?.name ?? "", options: element.props?.options ?? {} });
  }
  collectScreens(element.props?.children);
}

function renderLayout() {
  state.captured.length = 0;
  collectScreens(WorkspaceLayout());
}

function screen(name: string): Record<string, unknown> {
  const entry = state.captured.find((s) => s.name === name);
  if (!entry) throw new Error(`Stack.Screen ${name} 未注册`);
  return entry.options;
}

describe("WorkspaceLayout 嵌套 picker 呈现（RUYI-623）", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    state.useQuery.mockReturnValue({
      data: [{ id: "ws-1", slug: "acme" }],
      isLoading: false,
    });
  });

  it("Android：modal 之上的 picker 全部全屏 modal 化，sheet 专属选项不残留", () => {
    state.platformOS = "android";
    renderLayout();
    for (const name of NESTED_PICKERS) {
      const options = screen(name);
      expect(options.presentation, name).toBe("modal");
      expect(options, name).not.toHaveProperty("sheetAllowedDetents");
      expect(options, name).not.toHaveProperty("sheetGrabberVisible");
      expect(options, name).not.toHaveProperty("sheetInitialDetentIndex");
    }
  });

  it("Android：搜索头部 picker 保留 headerShown/title，body 自绘头部 picker 保持 headerShown=false", () => {
    state.platformOS = "android";
    renderLayout();
    expect(screen("new-issue-picker/status").headerShown).toBe(false);
    expect(screen("new-issue-picker/due-date").headerShown).toBe(false);
    expect(screen("new-project-picker/priority").headerShown).toBe(false);
    expect(screen("new-issue-picker/assignee").headerShown).toBe(true);
    expect(screen("new-issue-picker/assignee").title).toBe("Assignee");
    expect(screen("new-issue-picker/project").title).toBe("Project");
    expect(screen("new-issue-picker/actor").title).toBe("Created by");
  });

  it("Android：单层 formSheet 不受影响，runs/[taskId] 先例保持全屏 modal", () => {
    state.platformOS = "android";
    renderLayout();
    for (const name of SINGLE_LAYER_SHEETS) {
      // 本单契约是呈现路径不变（formSheet 不改 modal）；detent 等参数
      // 各屏允许文档化覆盖（如 chat-rename/menu 用 fitToContents），不在此断言。
      expect(screen(name).presentation, name).toBe("formSheet");
    }
    expect(screen("issue/[id]/runs/[taskId]").presentation).toBe("modal");
  });

  it("iOS：嵌套 picker 保持 sheet chrome 与搜索头部配置，runs/[taskId] 保持 formSheet", () => {
    state.platformOS = "ios";
    renderLayout();
    for (const name of NESTED_PICKERS) {
      const options = screen(name);
      expect(options.presentation, name).toBe("formSheet");
      expect(options.sheetGrabberVisible, name).toBe(true);
      expect(options.sheetAllowedDetents, name).toEqual([0.6, 0.95]);
    }
    expect(screen("new-issue-picker/assignee").headerShown).toBe(true);
    expect(screen("new-issue-picker/assignee").title).toBe("Assignee");
    expect(screen("new-issue-picker/project").sheetInitialDetentIndex).toBe("last");
    expect(screen("issue/[id]/runs/[taskId]").presentation).toBe("formSheet");
  });
});
