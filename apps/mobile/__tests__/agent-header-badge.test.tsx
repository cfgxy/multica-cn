import React from "react";
import { fireEvent, render, screen } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { notifyManager } from "@tanstack/react-query";

// Flush react-query's batched notifications synchronously — jest-expo's
// fake-timer environment never advances the default scheduler (same setup
// as issue-run-retry.test.tsx).
notifyManager.setScheduler((cb) => {
  cb();
  return 0;
});

const mockPush = jest.fn();
const mockListActive = jest.fn();
const mockListAll = jest.fn();

jest.mock("expo-router", () => ({
  router: { push: (...args: unknown[]) => mockPush(...args) },
}));

jest.mock("@/data/api", () => ({
  api: {
    listActiveTasksForIssue: (...args: unknown[]) => mockListActive(...args),
    listTasksByIssue: (...args: unknown[]) => mockListAll(...args),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({
      currentWorkspaceId: "workspace-1",
      currentWorkspaceSlug: "ws",
    }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string, opts?: Record<string, unknown>) => {
      if (typeof fallback !== "string") return key;
      if (!opts) return fallback;
      return fallback.replace(/\{\{(\w+)\}\}/g, (_m, k: string) =>
        String(opts[k] ?? ""),
      );
    },
  }),
}));

jest.mock("@/components/ui/avatar-stack", () => ({
  AvatarStack: () => {
    const { View } = require("react-native");
    return <View testID="avatar-stack" />;
  },
}));

jest.mock("@/components/ui/pulse-dot", () => ({
  PulseDot: () => {
    const { View } = require("react-native");
    return <View testID="pulse-dot" />;
  },
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: ({ name }: { name: string }) => {
    const { Text } = require("react-native");
    return <Text>{String(name)}</Text>;
  },
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

import { AgentHeaderBadge } from "@/components/issue/agent-header-badge";
import { issueKeys } from "@/data/queries/issues";
import type { AgentTask } from "@multica/core/types";

function task(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    issue_id: "issue-1",
    agent_id: "agent-1",
    status: "running",
    kind: "comment",
    created_at: "2026-10-02T00:00:00Z",
    ...overrides,
  } as AgentTask;
}

const RUNS_ROUTE = {
  pathname: "/[workspace]/issue/[id]/runs",
  params: { workspace: "ws", id: "issue-1" },
};

function newQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
}

function seedTasks(
  queryClient: QueryClient,
  active: AgentTask[],
  all: AgentTask[],
) {
  // The api mocks answer the mount refetch with the same fixtures the cache
  // was seeded with, so a staleTime-0 background refresh can't clobber the
  // state under assertion mid-test.
  mockListActive.mockResolvedValue(active);
  mockListAll.mockResolvedValue(all);
  queryClient.setQueryData(issueKeys.activeTasks("workspace-1", "issue-1"), active);
  queryClient.setQueryData(issueKeys.tasks("workspace-1", "issue-1"), all);
}

async function renderBadge(queryClient: QueryClient) {
  await render(
    <QueryClientProvider client={queryClient}>
      <AgentHeaderBadge issueId="issue-1" />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  jest.clearAllMocks();
});

describe("AgentHeaderBadge", () => {
  it("keeps the ambient working badge while a task is active", async () => {
    const queryClient = newQueryClient();
    const running = [task({})];
    seedTasks(queryClient, running, running);

    await renderBadge(queryClient);

    const badge = await screen.findByLabelText("Agent working — open runs");
    expect(screen.getByTestId("pulse-dot")).toBeTruthy();

    fireEvent.press(badge);
    expect(mockPush).toHaveBeenCalledWith(RUNS_ROUTE);
  });

  it("keeps a runs entry visible when only past runs exist (RUYI-417)", async () => {
    // RUYI-417: the badge used to render only for active runs, which made the
    // runs page unreachable the moment a run finished. With run history on
    // record the header must keep an (idle-styled) entry.
    const queryClient = newQueryClient();
    const history = [
      task({ id: "task-done", status: "completed" }),
      task({ id: "task-failed", status: "failed" }),
    ];
    seedTasks(queryClient, [], history);

    await renderBadge(queryClient);

    const badge = await screen.findByLabelText("View past runs");
    expect(screen.getByText("Runs · 2")).toBeTruthy();
    expect(screen.queryByTestId("pulse-dot")).toBeNull();

    fireEvent.press(badge);
    expect(mockPush).toHaveBeenCalledWith(RUNS_ROUTE);
  });

  it("renders nothing when the issue has never had a run", async () => {
    const queryClient = newQueryClient();
    seedTasks(queryClient, [], []);

    await renderBadge(queryClient);

    expect(screen.queryByLabelText("Agent working — open runs")).toBeNull();
    expect(screen.queryByLabelText("View past runs")).toBeNull();
    expect(mockPush).not.toHaveBeenCalled();
  });
});
