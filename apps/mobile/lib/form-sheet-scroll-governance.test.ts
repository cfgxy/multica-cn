// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * RUYI-637 — formSheet 弹窗滚动治理的机械断言。
 *
 * 「二阶段伸缩弹窗只能单向滚动」的机理（apps/mobile/CLAUDE.md RUYI-476 条目
 * 已记载）：双档位 `[0.6, 0.95]` 下以 0.6 小档打开时，内容顶部的纵向手势被
 * detent 切换抢占（iOS `prefersScrollingExpandsWhenScrolledToEdge`、Android
 * `BottomSheetBehavior`）——上滑先涨高度再滚动、下滑收层而不滚内容；Android
 * 在最大档下，未开启 `nestedScrollEnabled` 的滚动容器同样被 BottomSheetBehavior
 * 抢走下滑手势（列表不回滚、弹层跟手收层）。RUYI-476 只修了两个 project
 * picker 路由，本单把配方收敛为两层机械契约：
 *
 *   1. 路由层：长内容 sheet（长列表搜索 picker、长文本编辑器）在
 *      `_layout.tsx` 以共享常量统一「打开即落最大档」——0.6 档仅作 grabber
 *      下拉停靠点。短内容 sheet 保持 SHEET_OPTIONS 两段式打开性格，不在本单
 *      擅自改成全高。
 *   2. body 层：所有带可滚容器的 formSheet body 一律开启
 *      `nestedScrollEnabled`（iOS no-op，Android 交出最大档下的纵向手势
 *      协调）。
 *
 * 手法同 workspace-stacked-picker-sheets.test.ts（RUYI-623）：mock 全部依赖
 * 后直接调用布局组件，遍历元素树收集 name/options 断言；body 层用
 * readFileSync 源码形态断言（同 comment-anchor-wiring.test.ts 先例——
 * RN 原生滚动行为跑不进 node lane，能机械钉住的只有接线本身）。
 */

const state = vi.hoisted(() => ({
  platformOS: "ios",
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
vi.mock("@/data/realtime/use-presence-realtime", () => ({ usePresenceRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-squads-realtime", () => ({ useSquadsRealtime: vi.fn() }));
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

/** 长内容 sheet：打开即落最大档（SHEET_OPTIONS + sheetInitialDetentIndex）。 */
const LONG_CONTENT_ROUTES = [
  // 长列表搜索 picker（RUYI-476 配方从 project picker 推广到全部同构路由）。
  "issue/[id]/picker/assignee",
  "issue/[id]/picker/label",
  "issue/[id]/picker/project",
  "mention-picker",
  "skill-picker",
  "new-issue-picker/assignee",
  "new-issue-picker/project",
  "new-issue-picker/actor",
  "project/[id]/picker/lead",
  "tasks-actor-picker",
  // 长文本编辑器：提示词编辑的第一交互是大段输入，半档打开既看不全、
  // 首个手势还会被 detent 抢走。
  "more/agents/[id]/edit-instructions",
  "more/squads/[id]/edit-instructions",
];

/** 短内容 sheet：保持 SHEET_OPTIONS 双档位打开性格（0.6 档即容纳/首字段在顶）。 */
const TWO_SNAP_ROUTES = [
  "issue/[id]/picker/status",
  "issue/[id]/picker/priority",
  "issue/[id]/picker/due-date",
  "new-issue-picker/status",
  "new-issue-picker/priority",
  "new-issue-picker/due-date",
  "new-project-picker/status",
  "new-project-picker/priority",
  "project/[id]/picker/status",
  "project/[id]/picker/priority",
  "more/agents/[id]/edit-profile",
  "more/agents/[id]/run-config",
  "issue/[id]/runs",
  "issues-filter",
  "decisions-filter",
];

/**
 * 带可滚容器的 formSheet body 全量清单：滚动容器的
 * `nestedScrollEnabled` 必须开启（Android 最大档下双向滚动的直接修复；
 * iOS no-op）。project-picker-body 是 RUYI-476 既有落地，一并钉住防回退。
 */
const SCROLLABLE_SHEET_BODIES = [
  "components/issue/pickers/assignee-picker-body.tsx",
  "components/issue/pickers/label-picker-body.tsx",
  "components/issue/pickers/mention-picker-body.tsx",
  "components/issue/pickers/priority-picker-body.tsx",
  "components/issue/pickers/project-picker-body.tsx",
  "components/issue/pickers/quick-create-actor-picker-body.tsx",
  "components/issue/pickers/status-picker-body.tsx",
  "components/issues/actor-filter-picker-body.tsx",
  "components/agents/env-editor.tsx",
  "components/squads/member-add-sheet.tsx",
  "app/(app)/[workspace]/skill-picker.tsx",
  "app/(app)/[workspace]/issues-filter.tsx",
  "app/(app)/[workspace]/decisions-filter.tsx",
  "app/(app)/[workspace]/switch-workspace.tsx",
  "app/(app)/[workspace]/inbox/[id].tsx",
  "app/(app)/[workspace]/issue/[id]/runs.tsx",
  "app/(app)/[workspace]/issue/[id]/runs/[taskId].tsx",
  "app/(app)/[workspace]/more/agents/[id]/edit-profile.tsx",
  "app/(app)/[workspace]/more/agents/[id]/edit-instructions.tsx",
  "app/(app)/[workspace]/more/agents/[id]/run-config.tsx",
  "app/(app)/[workspace]/more/agents/[id]/runtime-config.tsx",
  "app/(app)/[workspace]/more/agents/[id]/skills.tsx",
  "app/(app)/[workspace]/more/agents/[id]/access.tsx",
  "app/(app)/[workspace]/more/agents/[id]/custom-args.tsx",
  "app/(app)/[workspace]/more/agents/[id]/mcp.tsx",
  "app/(app)/[workspace]/more/agents/[id]/composio.tsx",
  "app/(app)/[workspace]/more/agents/[id]/integrations.tsx",
  "app/(app)/[workspace]/more/squads/[id]/edit-instructions.tsx",
  "app/(app)/[workspace]/more/squads/[id]/execution-profiles.tsx",
];

function source(relative: string): string {
  return readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), "..", relative),
    "utf8",
  );
}

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

function count(sourceText: string, pattern: RegExp): number {
  // split 会对捕获组内容额外分段（虚高计数），统一走 matchAll。
  return [...sourceText.matchAll(new RegExp(pattern.source, pattern.flags))].length;
}

describe("formSheet 滚动治理（RUYI-637）", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    state.useQuery.mockReturnValue({
      data: [{ id: "ws-1", slug: "acme" }],
      isLoading: false,
    });
  });

  it("iOS：长内容 sheet 全部以最大档打开，且共享双档位 chrome", () => {
    state.platformOS = "ios";
    renderLayout();
    for (const name of LONG_CONTENT_ROUTES) {
      const options = screen(name);
      expect(options.sheetInitialDetentIndex, name).toBe("last");
      expect(options.presentation, name).toBe("formSheet");
      expect(options.sheetAllowedDetents, name).toEqual([0.6, 0.95]);
    }
  });

  it("iOS：短内容 sheet 保持两段式打开性格，不擅自全高", () => {
    state.platformOS = "ios";
    renderLayout();
    for (const name of TWO_SNAP_ROUTES) {
      expect(screen(name).sheetInitialDetentIndex, name).toBeUndefined();
    }
  });

  it("Android：modal 之上的嵌套 picker 不残留 sheetInitialDetentIndex（RUYI-623 契约不被本单破坏）", () => {
    state.platformOS = "android";
    renderLayout();
    for (const name of [
      "new-issue-picker/assignee",
      "new-issue-picker/project",
      "new-issue-picker/actor",
    ]) {
      const options = screen(name);
      expect(options.presentation, name).toBe("modal");
      expect(options, name).not.toHaveProperty("sheetInitialDetentIndex");
    }
  });

  it("可滚 formSheet body 全量开启 nestedScrollEnabled，且覆盖每个滚动容器", () => {
    for (const file of SCROLLABLE_SHEET_BODIES) {
      const text = source(file);
      expect(
        count(text, /nestedScrollEnabled/g),
        `${file} 缺 nestedScrollEnabled（Android 最大档下滚动容器会被 BottomSheetBehavior 抢走下滑手势）`,
      ).toBeGreaterThanOrEqual(
        count(text, /<(FlatList|ScrollView|SectionList)/g),
      );
    }
  });
});
