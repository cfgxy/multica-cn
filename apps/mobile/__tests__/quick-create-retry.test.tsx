// Retry entry for failed agent quick-creates (RUYI-527). Mirrors web's
// packages/views/inbox/components/inbox-page.tsx retry gate: a failed,
// issue-less quick-create inbox item whose details carry `task_id` +
// `source_context_id` gets a "retry with context" action that re-enqueues
// the ORIGINAL input server-side (POST /api/tasks/:id/retry-source-context —
// the server atomically transfers the pending source context, so the client
// only needs the original task's id).
//
// These tests pin the three layers:
//   1. `getQuickCreateRetryPlan` — failure-state recognition + which fields
//      the retry reuses from the item's details (task_id, original_prompt).
//   2. `useRetrySourceContextQuickCreate` — retry-request construction and
//      the post-settle inbox invalidation that surfaces the new task.
//   3. The inbox detail card — the button appears only for retryable
//      failures and fires the task-scoped request with structured error
//      alerts (unavailable / issue limit / generic).
import React from "react";
import { Alert } from "react-native";
import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { InboxItem } from "@multica/core/types";

const mockListInbox = jest.fn();
const mockRetryQuickCreate = jest.fn();

// Minimal stand-in for the real ApiError: production code branches on
// `err instanceof ApiError` to decode the structured body, so the mock must
// export a class the rejected values actually instantiate — the component
// under test imports this same mock, and the instanceof check hits it.
class MockApiError extends Error {
  readonly status: number;
  readonly body?: unknown;
  constructor(message: string, status: number, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

jest.mock("@/data/api", () => ({
  // Lazy getter: the mock factory runs during the import phase, before the
  // class declaration below has evaluated — an eager reference would hand
  // consumers `undefined` and blow up the instanceof branch inside onRetry.
  get ApiError() {
    return MockApiError;
  },
  api: {
    listInbox: (...args: unknown[]) => mockListInbox(...args),
    retrySourceContextQuickCreate: (...args: unknown[]) =>
      mockRetryQuickCreate(...args),
  },
}));

jest.mock("expo-router", () => ({
  router: { back: jest.fn(), push: jest.fn() },
  useLocalSearchParams: () => ({ id: "item-1" }),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (
    selector: (state: Record<string, unknown>) => unknown,
  ) =>
    selector({
      currentWorkspaceId: "ws-1",
      currentWorkspaceSlug: "ws",
    }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string) =>
      typeof fallback === "string" ? fallback : key,
  }),
}));

// The billing queries stay disabled for non-quota items, but the hooks still
// construct their options on render — stand them in with inert query shapes.
jest.mock("@/data/queries/billing", () => ({
  appConfigOptions: () => ({ queryKey: ["app-config"], enabled: false }),
  workspaceSubscriptionSummaryOptions: () => ({
    queryKey: ["ws-sub", "ws-1"],
    enabled: false,
  }),
}));

jest.mock("@/components/inbox/detail-label", () => ({
  useTypeLabels: () => ({}),
}));

jest.mock("@/lib/time-ago", () => ({
  timeAgo: () => "5m",
}));

// jest transform never sees @rn-primitives internals — stand in plain RN
// equivalents (decision-card / inbox-row pattern). Button → Pressable keeps
// children (the button label) findable via findByText.
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return { Text };
});

jest.mock("@/components/ui/button", () => {
  const { Pressable } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    Button: ({
      variant: _v,
      size: _s,
      ...props
    }: React.ComponentProps<typeof Pressable> & {
      variant?: string;
      size?: string;
    }) => <Pressable {...props} />,
  };
});

jest.mock("@/components/ui/icon-button", () => ({
  IconButton: () => null,
}));

import InboxNoticeDetail from "@/app/(app)/[workspace]/inbox/[id]";
import { getQuickCreateRetryPlan } from "@/lib/quick-create-retry";
import { useRetrySourceContextQuickCreate } from "@/data/mutations/inbox";
import { inboxKeys } from "@/data/queries/inbox";
import { router } from "expo-router";
import { notifyManager } from "@tanstack/react-query";

// Flush react-query's batched notifications synchronously — same reason as
// issue-run-retry.test.tsx: the jest-expo fake-timer environment never
// advances the default scheduler, so mutation state would never settle.
notifyManager.setScheduler((cb) => {
  cb();
  return 0;
});

function inboxItem(overrides: Partial<InboxItem> = {}): InboxItem {
  return {
    id: "item-1",
    workspace_id: "ws-1",
    recipient_type: "member",
    recipient_id: "user-1",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "quick_create_failed",
    severity: "attention",
    issue_id: null,
    title: "MCP连上了，也能看到对应的工具，怎么还提示授权？",
    body: "zcode session/prompt failed: zcode-acp process exited",
    issue_status: null,
    issue_identifier: null,
    read: false,
    archived: false,
    created_at: "2026-10-07T21:00:00Z",
    details: {
      original_prompt: "帮我创建一个修复登录循环的任务",
      source_context_id: "ctx-1",
      task_id: "task-1",
      agent_id: "agent-1",
    },
    ...overrides,
  };
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
  mockRetryQuickCreate.mockResolvedValue({ id: "task-new", status: "queued" });
});

describe("getQuickCreateRetryPlan — failure-state recognition", () => {
  it("recognizes a retryable failure and reuses the original input fields", () => {
    const plan = getQuickCreateRetryPlan(inboxItem());
    expect(plan).toEqual({
      taskId: "task-1",
      sourceContextId: "ctx-1",
      originalPrompt: "帮我创建一个修复登录循环的任务",
    });
  });

  it("rejects non-failure quick-create outcomes", () => {
    // done / unconfirmed never retry: done already produced an issue, and an
    // unconfirmed outcome may have too (web parity — no failure framing).
    expect(
      getQuickCreateRetryPlan(inboxItem({ type: "quick_create_done" })),
    ).toBeNull();
    expect(
      getQuickCreateRetryPlan(inboxItem({ type: "quick_create_unconfirmed" })),
    ).toBeNull();
  });

  it("rejects items without quick-create details (server pre-dates projections)", () => {
    expect(getQuickCreateRetryPlan(inboxItem({ details: null }))).toBeNull();
    expect(
      getQuickCreateRetryPlan(
        inboxItem({ details: { original_prompt: "only prompt" } }),
      ),
    ).toBeNull();
    expect(
      getQuickCreateRetryPlan(
        inboxItem({ details: { task_id: "task-1", original_prompt: "x" } }),
      ),
    ).toBeNull();
  });

  it("rejects ordinary issue-bound inbox items", () => {
    expect(
      getQuickCreateRetryPlan(
        inboxItem({ type: "issue_assigned", issue_id: "issue-1" }),
      ),
    ).toBeNull();
  });

  it("still plans a retry when the original prompt is absent", () => {
    // The server reuses the stored source context, not the prompt text — a
    // missing original_prompt degrades the read-only block, not the retry.
    const plan = getQuickCreateRetryPlan(
      inboxItem({ details: { task_id: "task-1", source_context_id: "ctx-1" } }),
    );
    expect(plan).not.toBeNull();
    expect(plan?.taskId).toBe("task-1");
  });
});

describe("useRetrySourceContextQuickCreate — retry request construction", () => {
  it("targets the failed task's id so the server reuses its original input", async () => {
    const queryClient = newQueryClient();
    const { result, unmount } = await renderHook(
      () => useRetrySourceContextQuickCreate(),
      { wrapper: wrapperFor(queryClient) },
    );

    await act(async () => {
      await result.current.mutateAsync("task-1");
    });

    expect(mockRetryQuickCreate).toHaveBeenCalledTimes(1);
    expect(mockRetryQuickCreate).toHaveBeenCalledWith("task-1");
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });

  it("invalidates the inbox cache once settled so the retried task surfaces", async () => {
    const queryClient = newQueryClient();
    queryClient.setQueryData(inboxKeys.list("ws-1"), [inboxItem()]);

    const { result, unmount } = await renderHook(
      () => useRetrySourceContextQuickCreate(),
      { wrapper: wrapperFor(queryClient) },
    );
    await act(async () => {
      await result.current.mutateAsync("task-1");
    });

    expect(
      queryClient.getQueryState(inboxKeys.list("ws-1"))?.isInvalidated,
    ).toBe(true);
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });

  it("propagates structured errors to the caller for branching", async () => {
    const queryClient = newQueryClient();
    mockRetryQuickCreate.mockRejectedValue(
      new MockApiError("conflict", 409, {
        code: "source_context_retry_unavailable",
      }),
    );
    const { result, unmount } = await renderHook(
      () => useRetrySourceContextQuickCreate(),
      { wrapper: wrapperFor(queryClient) },
    );

    await expect(
      act(async () => {
        await result.current.mutateAsync("task-1");
      }),
    ).rejects.toMatchObject({ body: { code: "source_context_retry_unavailable" } });
    await act(async () => {
      unmount();
    });
    queryClient.clear();
  });
});

describe("Inbox detail card retry entry", () => {
  async function renderDetail(item: InboxItem) {
    const queryClient = newQueryClient();
    queryClient.setQueryData(inboxKeys.list("ws-1"), [item]);
    const utils = await render(<InboxNoticeDetail />, {
      wrapper: wrapperFor(queryClient),
    });
    return { ...utils, queryClient };
  }

  it("renders the retry button on a retryable failure", async () => {
    await renderDetail(inboxItem());
    expect(await screen.findByText("Retry with context")).toBeTruthy();
  });

  it("renders no retry entry for done / unconfirmed outcomes", async () => {
    await renderDetail(inboxItem({ type: "quick_create_done" }));
    expect(screen.queryByText("Retry with context")).toBeNull();

    await renderDetail(inboxItem({ type: "quick_create_unconfirmed" }));
    expect(screen.queryByText("Retry with context")).toBeNull();
  });

  it("renders no retry entry when the failure predates source-context details", async () => {
    await renderDetail(inboxItem({ details: null }));
    expect(screen.queryByText("Retry with context")).toBeNull();
  });

  it("fires the task-scoped retry and returns to the inbox on success", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await renderDetail(inboxItem({ details: { task_id: "task-9", source_context_id: "ctx-9", original_prompt: "p" } }));

    fireEvent.press(await screen.findByText("Retry with context"));
    await waitFor(() =>
      expect(mockRetryQuickCreate).toHaveBeenCalledWith("task-9"),
    );
    await waitFor(() => {
      expect(alertSpy).toHaveBeenCalledWith(
        "Retry started with the original context",
      );
      expect(router.back).toHaveBeenCalled();
    });
    alertSpy.mockRestore();
  });

  it("surfaces the unavailable alert when the context can no longer retry", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockRetryQuickCreate.mockRejectedValue(
      new MockApiError("conflict", 409, {
        code: "source_context_retry_unavailable",
      }),
    );
    await renderDetail(inboxItem());

    fireEvent.press(await screen.findByText("Retry with context"));
    await waitFor(() =>
      expect(alertSpy).toHaveBeenCalledWith(
        "This context can no longer be retried. Start again from the branch point.",
      ),
    );
    expect(router.back).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("surfaces the issue-limit alert when the workspace is at capacity", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockRetryQuickCreate.mockRejectedValue(
      new MockApiError("limit", 403, { code: "issue_limit_reached" }),
    );
    await renderDetail(inboxItem());

    fireEvent.press(await screen.findByText("Retry with context"));
    await waitFor(() =>
      expect(alertSpy).toHaveBeenCalledWith(
        "This workspace has reached its issue limit",
      ),
    );
    alertSpy.mockRestore();
  });

  it("surfaces the generic failure alert otherwise", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockRetryQuickCreate.mockRejectedValue(new Error("network down"));
    await renderDetail(inboxItem());

    fireEvent.press(await screen.findByText("Retry with context"));
    await waitFor(() =>
      expect(alertSpy).toHaveBeenCalledWith(
        "Could not retry with the original context",
      ),
    );
    alertSpy.mockRestore();
  });
});
