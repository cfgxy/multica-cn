import React from "react";
import { View } from "react-native";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react-native";
import { QueryClient, QueryClientProvider, notifyManager } from "@tanstack/react-query";

const mockCancelTaskById = jest.fn();
const mockRetryIssueRun = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    cancelTaskById: (...args: unknown[]) => mockCancelTaskById(...args),
    retryIssueRun: (...args: unknown[]) => mockRetryIssueRun(...args),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1", currentWorkspaceSlug: "ws" }),
}));

// Sever the auth-store → server-store → AsyncStorage (native) import chain
// that run-row's issue mutations pull in.
jest.mock("@/data/auth-store", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "member-1" } }),
}));

jest.mock("@/data/use-actor-name", () => ({
  useActorLookup: () => ({ getName: () => "Agent A" }),
}));

// Interpolating stand-in for i18next so the short-id fallback renders its
// {{prefix}} like production does.
jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string, opts?: Record<string, unknown>) =>
      typeof fallback === "string"
        ? fallback.replace(/\{\{(\w+)\}\}/g, (_m, name: string) =>
            String(opts?.[name] ?? ""),
          )
        : key,
  }),
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("expo-router", () => ({
  router: { push: jest.fn() },
}));

// RNGH keeps renderRightActions out of the tree until a real drag opens the
// row — a gesture jest can't perform. Mock it reveal-always so the
// destructive action is directly pressable; the drag/haptics behaviour is
// the inbox row's production concern, not this test's.
jest.mock("react-native-gesture-handler/ReanimatedSwipeable", () => {
  const React = require("react");
  const { View } = require("react-native");
  const MockSwipeable = React.forwardRef(function MockSwipeable(
    props: {
      children?: React.ReactNode;
      renderRightActions?: (progress: unknown, drag: unknown) => React.ReactNode;
    },
    _ref: unknown,
  ) {
    return (
      <View>
        {props.children}
        {props.renderRightActions?.({ value: -80 }, { value: -80 })}
      </View>
    );
  });
  return { __esModule: true, default: MockSwipeable };
});

jest.mock("react-native-reanimated", () => {
  const React = require("react");
  const { View } = require("react-native");
  // A real component with a displayName — NativeWind's css-interop wrapper
  // reads it for every animated JSX element.
  const AnimatedView = React.forwardRef(function AnimatedView(props, ref) {
    const { children, ...rest } = props;
    return (
      <View ref={ref} {...rest}>
        {children}
      </View>
    );
  });
  AnimatedView.displayName = "AnimatedView";
  return {
    __esModule: true,
    default: Object.assign(AnimatedView, {
      View: AnimatedView,
      create: () => AnimatedView,
    }),
    View: AnimatedView,
    // The shared-value primitives PulseDot (running-row indicator) needs;
    // animation timing is inert in jest.
    useSharedValue: (initial: number) => ({ value: initial }),
    useAnimatedStyle: () => ({}),
    useAnimatedReaction: () => {},
    withRepeat: (animation: unknown) => animation,
    withTiming: (value: unknown) => value,
    runOnJS: (fn: unknown) => fn,
  };
});

jest.mock("expo-haptics", () => ({
  impactAsync: jest.fn().mockResolvedValue(undefined),
  notificationAsync: jest.fn().mockResolvedValue(undefined),
  ImpactFeedbackStyle: { Medium: "medium" },
}));

import { SwipeableAgentTaskRow } from "@/components/agents/swipeable-agent-task-row";
import { AgentRunHistoryRow } from "@/components/agents/agent-run-history-row";
import { useCancelAgentTask, useRetryAgentRun } from "@/data/mutations/agents";
import { agentTaskSnapshotKeys } from "@/data/queries/agent-task-snapshot";
import { agentTasksKeys } from "@/data/queries/agent-tasks";
import { issueKeys } from "@/data/queries/issues";
import type { AgentTask } from "@multica/core/types";

// Flush react-query's batched notifications synchronously — jest-expo's
// fake timers never advance the default scheduler (see issue-run-retry).
notifyManager.setScheduler((cb) => {
  cb();
  return 0;
});

function task(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    issue_id: "issue-0001-aaaa",
    agent_id: "agent-1",
    status: "completed",
    completed_at: "2026-10-02T00:00:00Z",
    created_at: "2026-10-01T00:00:00Z",
    ...overrides,
  } as AgentTask;
}

function wrapperFor(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
  };
}

function newQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

beforeEach(() => {
  jest.clearAllMocks();
  mockCancelTaskById.mockResolvedValue(undefined);
  mockRetryIssueRun.mockResolvedValue({ id: "task-1", status: "queued" });
});

describe("useCancelAgentTask", () => {
  it("cancels exactly the one task id it is handed", async () => {
    const queryClient = newQueryClient();
    const { result, unmount } = await renderHook(() => useCancelAgentTask(), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync("task-A");
    });

    expect(mockCancelTaskById).toHaveBeenCalledTimes(1);
    expect(mockCancelTaskById).toHaveBeenCalledWith("task-A");
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });

  it("sweeps the presence snapshot and the per-agent task list on settle", async () => {
    const queryClient = newQueryClient();
    queryClient.setQueryData(agentTaskSnapshotKeys.all("workspace-1"), []);
    queryClient.setQueryData(agentTasksKeys.all("workspace-1"), []);

    const { result, unmount } = await renderHook(() => useCancelAgentTask(), {
      wrapper: wrapperFor(queryClient),
    });
    await act(async () => {
      await result.current.mutateAsync("task-A");
    });

    expect(
      queryClient.getQueryState(agentTaskSnapshotKeys.all("workspace-1"))
        ?.isInvalidated,
    ).toBe(true);
    expect(
      queryClient.getQueryState(agentTasksKeys.all("workspace-1"))
        ?.isInvalidated,
    ).toBe(true);
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });
});

describe("useRetryAgentRun", () => {
  it("retries through the run-level endpoint with the run's source issue", async () => {
    const queryClient = newQueryClient();
    const { result, unmount } = await renderHook(() => useRetryAgentRun(), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync({ issueId: "issue-1", taskId: "task-9" });
    });

    expect(mockRetryIssueRun).toHaveBeenCalledTimes(1);
    expect(mockRetryIssueRun).toHaveBeenCalledWith("issue-1", "task-9");
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });

  it("invalidates the per-agent list and the source issue's runs sheet", async () => {
    const queryClient = newQueryClient();
    queryClient.setQueryData(agentTasksKeys.all("workspace-1"), []);
    queryClient.setQueryData(issueKeys.tasks("workspace-1", "issue-1"), []);

    const { result, unmount } = await renderHook(() => useRetryAgentRun(), {
      wrapper: wrapperFor(queryClient),
    });
    await act(async () => {
      await result.current.mutateAsync({ issueId: "issue-1", taskId: "task-9" });
    });

    expect(
      queryClient.getQueryState(agentTasksKeys.all("workspace-1"))
        ?.isInvalidated,
    ).toBe(true);
    expect(
      queryClient.getQueryState(issueKeys.tasks("workspace-1", "issue-1"))
        ?.isInvalidated,
    ).toBe(true);
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });
});

describe("AgentRunHistoryRow", () => {
  async function renderRow(overrides: Partial<AgentTask>, issueTitle: string | null = null) {
    const queryClient = newQueryClient();
    const utils = await render(
      <AgentRunHistoryRow
        task={task(overrides)}
        issueTitle={issueTitle}
        wsSlug="ws"
      />,
      { wrapper: wrapperFor(queryClient) },
    );
    return { ...utils, queryClient };
  }

  it("shows the resolved issue title", async () => {
    await renderRow({ status: "completed" }, "Fix the login flow");
    expect(screen.getByText("Fix the login flow")).toBeTruthy();
  });

  it("falls back to the issue short id when the title is unresolved (no 任务不可见 placeholder)", async () => {
    await renderRow({ status: "completed", issue_id: "issue-0001-aaaa" }, null);
    expect(screen.getByText("Issue issue-00…")).toBeTruthy();
  });

  it("keeps the source-label vocabulary for issue-less runs", async () => {
    await renderRow({ status: "completed", issue_id: "", chat_session_id: "chat-1" }, null);
    expect(screen.getByText("Chat task")).toBeTruthy();
  });

  it("fires the run-scoped retry immediately for a failed run", async () => {
    await renderRow({ status: "failed", id: "task-9", issue_id: "issue-1" });

    fireEvent.press(await screen.findByLabelText("Retry task"));
    await waitFor(() =>
      expect(mockRetryIssueRun).toHaveBeenCalledWith("issue-1", "task-9"),
    );
  });

  it("routes a cancelled run through a Run-again confirmation first", async () => {
    const { Alert } = require("react-native");
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await renderRow({ status: "cancelled", id: "task-5", issue_id: "issue-1" });

    const button = await screen.findByLabelText("Run again");
    fireEvent.press(button);
    expect(mockRetryIssueRun).not.toHaveBeenCalled();

    const buttons = alertSpy.mock.calls[0][2] as Array<{
      onPress?: () => void;
      style?: string;
    }>;
    const confirm = buttons.find((b) => b.style === "destructive");
    expect(confirm).toBeTruthy();
    // Fire the confirm callback outside a hand-rolled async act: settling
    // the retry mutation inside `await act(async …)` poisons React 19's
    // act queue — every later render in this file then silently commits
    // an empty tree. Sync act + waitFor drains it (same shape as the
    // failed-retry test above).
    act(() => {
      confirm!.onPress?.();
    });
    await waitFor(() =>
      expect(mockRetryIssueRun).toHaveBeenCalledWith("issue-1", "task-5"),
    );
    alertSpy.mockRestore();
  });

  it("renders no retry entry on completed runs or issue-less terminal runs", async () => {
    await renderRow({ status: "completed" });
    expect(screen.queryByLabelText("Retry task")).toBeNull();
    expect(screen.queryByLabelText("Run again")).toBeNull();

    await renderRow({ status: "cancelled", issue_id: "" });
    expect(screen.queryByLabelText("Run again")).toBeNull();
  });

  it("pushes the run detail sheet for issue-linked rows", async () => {
    const { router } = require("expo-router");
    await renderRow(
      { status: "completed", id: "task-7", issue_id: "issue-1" },
      "Fix the login flow",
    );
    await screen.findByText("Fix the login flow");
    fireEvent.press(await screen.findByText("Fix the login flow"));
    expect(router.push).toHaveBeenCalledWith(
      expect.objectContaining({
        pathname: "/[workspace]/issue/[id]/runs/[taskId]",
        params: expect.objectContaining({ taskId: "task-7" }),
      }),
    );
  });
});

describe("SwipeableAgentTaskRow per-task cancel", () => {
  // Both rows share one mutation instance exactly like the page wires them:
  // each row's onCancel carries only its own task id, so a swipe-fire on A
  // must never send B's.
  function CancelHarness() {
    const cancel = useCancelAgentTask();
    return (
      <View>
        <SwipeableAgentTaskRow
          task={task({ id: "task-A", issue_id: "issue-1", status: "running" })}
          issueTitle="Issue A"
          wsSlug="ws"
          cancellable
          onCancel={() => cancel.mutate("task-A")}
        />
        <SwipeableAgentTaskRow
          task={task({ id: "task-B", issue_id: "issue-2", status: "queued" })}
          issueTitle="Issue B"
          wsSlug="ws"
          cancellable
          onCancel={() => cancel.mutate("task-B")}
        />
      </View>
    );
  }

  it("cancels only the row whose action was pressed — neighbours untouched", async () => {
    const queryClient = newQueryClient();
    await render(<CancelHarness />, { wrapper: wrapperFor(queryClient) });

    const actions = await screen.findAllByLabelText("Cancel task");
    expect(actions).toHaveLength(2);
    fireEvent.press(actions[0]);

    await waitFor(() => expect(mockCancelTaskById).toHaveBeenCalledTimes(1));
    expect(mockCancelTaskById).toHaveBeenCalledWith("task-A");
    expect(mockCancelTaskById).not.toHaveBeenCalledWith("task-B");
  });

  it("renders the plain row — no cancel action — when not cancellable", async () => {
    const queryClient = newQueryClient();
    await render(
      <SwipeableAgentTaskRow
        task={task({ id: "task-C", status: "running" })}
        issueTitle="Issue C"
        wsSlug="ws"
        cancellable={false}
        onCancel={jest.fn()}
      />,
      { wrapper: wrapperFor(queryClient) },
    );
    expect(screen.queryByLabelText("Cancel task")).toBeNull();
  });
});

describe("agent detail batch-cancel removal (source contract)", () => {
  it("the page no longer references the batch cancel affordances", () => {
    const page = readFileSync(
      resolve(__dirname, "../app/(app)/[workspace]/more/agents/[id].tsx"),
      "utf8",
    );
    expect(page).not.toMatch(/cancel_all_tasks/);
    expect(page).not.toMatch(/useCancelAgentTasks/);
    expect(page).not.toMatch(/cancelAgentTasks/);
    expect(page).not.toMatch(/confirmCancelAll/);
    // …and the per-task swipe row is what replaced it.
    expect(page).toMatch(/SwipeableAgentTaskRow/);
  });
});
