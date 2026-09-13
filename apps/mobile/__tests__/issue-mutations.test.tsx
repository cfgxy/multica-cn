import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TimelineQueryData } from "@multica/core/issues/timeline-query";

const mockCreateComment = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    createComment: (...args: unknown[]) => mockCreateComment(...args),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "member-1" } }),
}));

jest.mock("@/data/stores/failed-comments-store", () => ({
  useFailedCommentsStore: {
    getState: () => ({ markFailed: jest.fn(), clear: jest.fn() }),
  },
}));

jest.mock("i18next", () => ({
  __esModule: true,
  default: { t: (_key: string, fallback: string) => fallback },
}));

import { useCreateComment } from "@/data/mutations/issues";
import { issueKeys } from "@/data/queries/issues";

function wrapperFor(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
  };
}

describe("useCreateComment timeline cache", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockCreateComment.mockResolvedValue({
      id: "comment-1",
      issue_id: "issue-1",
      author_type: "member",
      author_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: "2026-09-05T09:00:00Z",
      updated_at: "2026-09-05T09:00:00Z",
      type: "comment",
      reactions: [],
      attachments: [],
    });
  });

  it("does not materialize an optimistic timeline when the cache is absent", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const key = issueKeys.timeline("workspace-1", "issue-1");
    const { result, unmount } = await renderHook(() => useCreateComment("issue-1"), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync({ content: "hello" });
    });

    expect(queryClient.getQueryData<TimelineQueryData>(key)).toBeUndefined();
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await unmount();
    queryClient.clear();
  });

  it("preserves truncation metadata while adding an optimistic comment", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const key = issueKeys.timeline("workspace-1", "issue-1");
    queryClient.setQueryData<TimelineQueryData>(key, {
      entries: [],
      truncatedKinds: ["activity"],
    });
    let resolveCreate: (value: unknown) => void = () => undefined;
    mockCreateComment.mockReturnValue(
      new Promise((resolve) => {
        resolveCreate = resolve;
      }),
    );
    const { result, unmount } = await renderHook(() => useCreateComment("issue-1"), {
      wrapper: wrapperFor(queryClient),
    });

    let mutationPromise: Promise<unknown> | undefined;
    await act(async () => {
      mutationPromise = result.current.mutateAsync({ content: "hello" });
      await Promise.resolve();
    });
    expect(queryClient.getQueryData<TimelineQueryData>(key)).toMatchObject({
      truncatedKinds: ["activity"],
      entries: [expect.objectContaining({ type: "comment", content: "hello" })],
    });

    await act(async () => {
      resolveCreate({
        id: "comment-1",
        issue_id: "issue-1",
        author_type: "member",
        author_id: "member-1",
        content: "hello",
        parent_id: null,
        created_at: "2026-09-05T09:00:00Z",
        updated_at: "2026-09-05T09:00:00Z",
        type: "comment",
        reactions: [],
        attachments: [],
      });
      await mutationPromise;
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await unmount();
    queryClient.clear();
  });
});
