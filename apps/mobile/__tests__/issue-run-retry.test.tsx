import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const mockRetryIssueRun = jest.fn();
const mockRerunIssue = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    retryIssueRun: (...args: unknown[]) => mockRetryIssueRun(...args),
    rerunIssue: (...args: unknown[]) => mockRerunIssue(...args),
    cancelTaskById: jest.fn(),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1", currentWorkspaceSlug: "ws" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "member-1" } }),
}));

jest.mock("@/data/use-actor-name", () => ({
  useActorLookup: () => ({ getName: () => "Agent A" }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string) =>
      typeof fallback === "string" ? fallback : key,
  }),
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

jest.mock("expo-router", () => ({
  router: { push: jest.fn() },
}));

jest.mock("i18next", () => ({
  __esModule: true,
  default: { t: (_key: string, fallback: string) => fallback },
}));

import { RunRow } from "@/components/issue/run-row";
import { useRerunIssue, useRetryIssueRun } from "@/data/mutations/issues";
import { issueKeys } from "@/data/queries/issues";
import { notifyManager } from "@tanstack/react-query";
import type { AgentTask } from "@multica/core/types";

// Flush react-query's batched notifications synchronously. The default
// scheduler defers through setTimeout, which jest-expo's fake-timer
// environment never advances — mutation state (the isPending flip, the
// onError alert) would never reach the component tree mid-test.
notifyManager.setScheduler((cb) => {
  cb();
  return 0;
});

function task(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    issue_id: "issue-1",
    agent_id: "agent-1",
    status: "failed",
    kind: "comment",
    created_at: "2026-10-02T00:00:00Z",
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
  mockRetryIssueRun.mockResolvedValue({ id: "task-1", status: "queued" });
  mockRerunIssue.mockResolvedValue({ id: "task-1", status: "queued" });
});

describe("useRetryIssueRun", () => {
  it("targets the specific run id, not an issue-level fallback", async () => {
    const queryClient = newQueryClient();
    const { result, unmount } = await renderHook(
      () => useRetryIssueRun("issue-1"),
      { wrapper: wrapperFor(queryClient) },
    );

    await act(async () => {
      await result.current.mutateAsync("run-42");
    });

    expect(mockRetryIssueRun).toHaveBeenCalledTimes(1);
    expect(mockRetryIssueRun).toHaveBeenCalledWith("issue-1", "run-42");
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });

  it("invalidates the run caches once settled", async () => {
    const queryClient = newQueryClient();
    queryClient.setQueryData(issueKeys.tasks("workspace-1", "issue-1"), []);
    queryClient.setQueryData(
      issueKeys.activeTasks("workspace-1", "issue-1"),
      [],
    );

    const { result, unmount } = await renderHook(
      () => useRetryIssueRun("issue-1"),
      { wrapper: wrapperFor(queryClient) },
    );
    await act(async () => {
      await result.current.mutateAsync("run-42");
    });

    expect(
      queryClient.getQueryState(issueKeys.tasks("workspace-1", "issue-1"))
        ?.isInvalidated,
    ).toBe(true);
    expect(
      queryClient.getQueryState(issueKeys.activeTasks("workspace-1", "issue-1"))
        ?.isInvalidated,
    ).toBe(true);
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });
});

describe("useRerunIssue", () => {
  it("sends the task-scoped rerun with an explicit task id", async () => {
    const queryClient = newQueryClient();
    const { result, unmount } = await renderHook(() => useRerunIssue("issue-1"), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync("task-7");
    });

    expect(mockRerunIssue).toHaveBeenCalledTimes(1);
    expect(mockRerunIssue).toHaveBeenCalledWith("issue-1", "task-7");
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });
});

describe("RunRow retry entry", () => {
  // RetryButton calls useMutation internally, so every render rides a
  // QueryClientProvider — same wrapper the hook tests above use.
  async function renderRunRow(overrides: Partial<AgentTask>) {
    const queryClient = newQueryClient();
    const utils = await render(<RunRow task={task(overrides)} issueId="issue-1" />, {
      wrapper: wrapperFor(queryClient),
    });
    return { ...utils, queryClient };
  }

  it("surfaces a permission block differently from a generic failure", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockRetryIssueRun.mockRejectedValue(
      Object.assign(new Error("request failed"), {
        body: { reason_code: "invocation_not_allowed" },
      }),
    );
    await renderRunRow({ status: "failed", id: "task-2" });

    fireEvent.press(await screen.findByLabelText("Retry task"));
    await waitFor(() => expect(alertSpy).toHaveBeenCalledTimes(1));
    const [title, message] = alertSpy.mock.calls[0] as [string, string];
    expect(message).toMatch(/don't have permission/i);
    expect(title).toMatch(/failed to retry/i);
    alertSpy.mockRestore();
  });

  it("renders a retry button on a failed run", async () => {
    await renderRunRow({ status: "failed" });
    expect(screen.getByLabelText("Retry task")).toBeTruthy();
  });

  it("renders no retry entry on completed / active runs", async () => {
    const { rerender, queryClient } = await renderRunRow({
      status: "completed",
    });
    expect(screen.queryByLabelText("Retry task")).toBeNull();

    await rerender(
      <QueryClientProvider client={queryClient}>
        <RunRow task={task({ status: "running" })} issueId="issue-1" />
      </QueryClientProvider>,
    );
    expect(screen.queryByLabelText("Retry task")).toBeNull();
  });

  it("fires the run-scoped retry immediately for a failed run", async () => {
    await renderRunRow({ status: "failed", id: "task-9" });
    fireEvent.press(screen.getByLabelText("Retry task"));

    await waitFor(() =>
      expect(mockRetryIssueRun).toHaveBeenCalledWith("issue-1", "task-9"),
    );
  });

  it("routes a cancelled run through a Run-again confirmation first", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await renderRunRow({ status: "cancelled", id: "task-5" });

    const button = screen.getByLabelText("Run again");
    expect(button).toBeTruthy();
    fireEvent.press(button);
    expect(mockRetryIssueRun).not.toHaveBeenCalled();

    // The confirm dialog's destructive action carries the retry.
    const buttons = alertSpy.mock.calls[0][2] as Array<{
      onPress?: () => void;
      style?: string;
    }>;
    const confirm = buttons.find((b) => b.style === "destructive");
    expect(confirm).toBeTruthy();
    await act(async () => {
      confirm!.onPress?.();
    });
    expect(mockRetryIssueRun).toHaveBeenCalledWith("issue-1", "task-5");
    alertSpy.mockRestore();
  });

  it("disables the button while a retry is in flight (no double fire)", async () => {
    // Deferred (not a never-settling promise): a promise left hanging
    // across tests poisons the next render pass — the retry button in the
    // following test then never commits within findBy's window.
    let settle!: (v: unknown) => void;
    mockRetryIssueRun.mockImplementation(
      () =>
        new Promise((resolve) => {
          settle = resolve;
        }),
    );
    await renderRunRow({ status: "failed", id: "task-3" });

    // findBy* rides waitFor: React 19 concurrent rendering may not have
    // committed the first frame synchronously after `await render`.
    fireEvent.press(await screen.findByLabelText("Retry task"));
    // The pending flip replaces the Pressable; RN absorbs `disabled` into
    // accessibilityState, so assert there, re-querying the live node.
    await waitFor(() => {
      expect(screen.getByLabelText("Retry task").props.accessibilityState)
        .toMatchObject({ disabled: true });
    });

    fireEvent.press(screen.getByLabelText("Retry task"));
    expect(mockRetryIssueRun).toHaveBeenCalledTimes(1);

    // Let the in-flight mutation land before the auto-cleanup unmounts the
    // tree, so no pending react-query state leaks into the next test.
    await act(async () => {
      settle({ id: "task-3", status: "queued" });
    });
  });

});
