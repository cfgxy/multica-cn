// @ts-nocheck
import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const mockUpdateAgent = jest.fn();

jest.mock("@/data/api", () => ({
  api: { updateAgent: (...args) => mockUpdateAgent(...args) },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector) => selector({ currentWorkspaceId: "ws-1" }),
}));

import { useUpdateAgent } from "@/data/mutations/agents";
import { agentKeys } from "@/data/queries/agents";

function wrapperFor(queryClient) {
  return function Wrapper({ children }) {
    return (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
  };
}

function agent(overrides = {}) {
  return {
    id: "agent-1",
    name: "Coder",
    description: "does things",
    instructions: "be nice",
    avatar_url: null,
    runtime_id: "runtime-1",
    model: "gpt-x",
    thinking_level: null,
    service_tier: null,
    max_concurrent_tasks: -1,
    session_max_context_tokens: null,
    session_compact_pct: null,
    runtime_config: {},
    skills: [],
    ...overrides,
  };
}

describe("useUpdateAgent concurrency normalization", () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it("normalizes before an awaited optimistic update can replace the legacy cache value", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const detailKey = agentKeys.detail("ws-1", "agent-1");
    queryClient.setQueryData(detailKey, agent());

    let releaseFirstDetailCancel;
    const firstDetailCancel = new Promise((resolve) => {
      releaseFirstDetailCancel = resolve;
    });
    const cancelQueries = queryClient.cancelQueries.bind(queryClient);
    let detailCancelCount = 0;
    jest.spyOn(queryClient, "cancelQueries").mockImplementation((filters) => {
      if (
        JSON.stringify(filters.queryKey) === JSON.stringify(detailKey) &&
        detailCancelCount++ === 0
      ) {
        return firstDetailCancel;
      }
      return cancelQueries(filters);
    });

    mockUpdateAgent.mockImplementation(async (_id, patch) => {
      if (patch.max_concurrent_tasks === 3) {
        throw new Error("concurrent update failed");
      }
      return agent({ ...patch, max_concurrent_tasks: 0 });
    });

    const { result, unmount } = await renderHook(() => useUpdateAgent("agent-1"), {
      wrapper: wrapperFor(queryClient),
    });

    let firstMutation;
    await act(async () => {
      firstMutation = result.current.mutateAsync({ instructions: "updated" });
      await Promise.resolve();
    });

    await act(async () => {
      await expect(
        result.current.mutateAsync({ max_concurrent_tasks: 3 }),
      ).rejects.toThrow("concurrent update failed");
    });

    expect(queryClient.getQueryData(detailKey).max_concurrent_tasks).toBe(-1);
    releaseFirstDetailCancel();

    await act(async () => {
      await firstMutation;
    });

    expect(mockUpdateAgent).toHaveBeenNthCalledWith(2, "agent-1", {
      instructions: "updated",
      max_concurrent_tasks: 0,
    });
    await waitFor(() =>
      expect(queryClient.getQueryData(detailKey).max_concurrent_tasks).toBe(0),
    );

    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });
});
