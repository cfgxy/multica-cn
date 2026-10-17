import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TimelineQueryData } from "@multica/core/issues/timeline-query";
import type { TimelineEntry } from "@multica/core/types";

const mockCreateComment = jest.fn();
const mockUpdateComment = jest.fn();
const mockMarkFailed = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    createComment: (...args: unknown[]) => mockCreateComment(...args),
    updateComment: (...args: unknown[]) => mockUpdateComment(...args),
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
    getState: () => ({ markFailed: mockMarkFailed, clear: jest.fn() }),
  },
}));

jest.mock("i18next", () => ({
  __esModule: true,
  default: { t: (_key: string, fallback: string) => fallback },
}));

import { useCreateComment, useEditComment } from "@/data/mutations/issues";
import { issueKeys } from "@/data/queries/issues";
import { reconcileCreatedComment } from "@/data/realtime/issue-ws-updaters";

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
    queryClient.unmount();
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
    expect(queryClient.getQueryData<TimelineQueryData>(key)).toMatchObject({
      truncatedKinds: ["activity"],
      entries: [expect.objectContaining({ id: "comment-1", content: "hello" })],
    });
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("keeps a failed optimistic comment available for retry", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const key = issueKeys.timeline("workspace-1", "issue-1");
    queryClient.setQueryData<TimelineQueryData>(key, {
      entries: [],
      truncatedKinds: [],
    });
    mockCreateComment.mockRejectedValueOnce(new Error("offline"));
    const { result, unmount } = await renderHook(() => useCreateComment("issue-1"), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync({ content: "hello" }).catch(() => undefined);
    });

    const entries = queryClient.getQueryData<TimelineQueryData>(key)?.entries;
    expect(entries).toHaveLength(1);
    expect(entries?.[0]).toMatchObject({
      id: expect.stringMatching(/^optimistic-/),
      content: "hello",
    });
    expect(mockMarkFailed).toHaveBeenCalledWith(
      entries?.[0].id,
      expect.objectContaining({ content: "hello", error: "offline" }),
    );
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("does not mark an ambiguous realtime success as failed when its HTTP response errors", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const key = issueKeys.timeline("workspace-1", "issue-1");
    queryClient.setQueryData<TimelineQueryData>(key, { entries: [], truncatedKinds: [] });
    let rejectFirst: (error: Error) => void = () => undefined;
    let resolveSecond: (value: unknown) => void = () => undefined;
    mockCreateComment
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectFirst = reject; }))
      .mockImplementationOnce(() => new Promise((resolve) => { resolveSecond = resolve; }));
    const { result, unmount } = await renderHook(() => useCreateComment("issue-1"), {
      wrapper: wrapperFor(queryClient),
    });

    let first: Promise<unknown> | undefined;
    let second: Promise<unknown> | undefined;
    await act(async () => {
      first = result.current.mutateAsync({ content: "hello" });
      second = result.current.mutateAsync({ content: "hello" });
      await Promise.resolve();
    });
    const optimisticIds = queryClient
      .getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id) ?? [];
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", {
      type: "comment",
      id: "comment-from-realtime",
      actor_type: "member",
      actor_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: "2026-09-05T09:00:00Z",
    });

    await act(async () => {
      rejectFirst(new Error("offline"));
      await first?.catch(() => undefined);
    });
    await act(async () => {
      resolveSecond({
        id: "comment-second",
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
      await second;
    });

    expect(optimisticIds).toHaveLength(2);
    expect(mockMarkFailed).not.toHaveBeenCalled();
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries).toHaveLength(2);
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("preserves two successful realtime comments when both HTTP responses error", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const key = issueKeys.timeline("workspace-1", "issue-1");
    queryClient.setQueryData<TimelineQueryData>(key, { entries: [], truncatedKinds: [] });
    let rejectFirst: (error: Error) => void = () => undefined;
    let rejectSecond: (error: Error) => void = () => undefined;
    mockCreateComment
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectFirst = reject; }))
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectSecond = reject; }));
    const { result, unmount } = await renderHook(() => useCreateComment("issue-1"), {
      wrapper: wrapperFor(queryClient),
    });

    let first: Promise<unknown> | undefined;
    let second: Promise<unknown> | undefined;
    await act(async () => {
      first = result.current.mutateAsync({ content: "hello" });
      second = result.current.mutateAsync({ content: "hello" });
      await Promise.resolve();
    });
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", {
      type: "comment",
      id: "comment-a",
      actor_type: "member",
      actor_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: "2026-09-05T09:00:00Z",
    });
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", {
      type: "comment",
      id: "comment-b",
      actor_type: "member",
      actor_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: "2026-09-05T09:00:01Z",
    });

    await act(async () => {
      rejectFirst(new Error("connection lost"));
      rejectSecond(new Error("connection lost"));
      await Promise.all([first?.catch(() => undefined), second?.catch(() => undefined)]);
    });

    expect(mockMarkFailed).not.toHaveBeenCalled();
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-a",
      "comment-b",
    ]);
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("keeps one real failure retryable when only one identical send has a realtime success", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const key = issueKeys.timeline("workspace-1", "issue-1");
    queryClient.setQueryData<TimelineQueryData>(key, { entries: [], truncatedKinds: [] });
    let rejectFirst: (error: Error) => void = () => undefined;
    let rejectSecond: (error: Error) => void = () => undefined;
    mockCreateComment
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectFirst = reject; }))
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectSecond = reject; }));
    const { result, unmount } = await renderHook(() => useCreateComment("issue-1"), {
      wrapper: wrapperFor(queryClient),
    });

    let first: Promise<unknown> | undefined;
    let second: Promise<unknown> | undefined;
    await act(async () => {
      first = result.current.mutateAsync({ content: "hello" });
      second = result.current.mutateAsync({ content: "hello" });
      await Promise.resolve();
    });
    const optimisticIds = queryClient
      .getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id) ?? [];
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", {
      type: "comment",
      id: "comment-saved",
      actor_type: "member",
      actor_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: "2026-09-05T09:00:00Z",
    });

    await act(async () => {
      rejectFirst(new Error("connection lost"));
      rejectSecond(new Error("offline"));
      await Promise.all([first?.catch(() => undefined), second?.catch(() => undefined)]);
    });

    expect(mockMarkFailed).toHaveBeenCalledTimes(1);
    expect(mockMarkFailed).toHaveBeenCalledWith(
      expect.stringMatching(/^optimistic-/),
      expect.objectContaining({ content: "hello", error: "offline" }),
    );
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-saved",
      expect.stringMatching(/^optimistic-/),
    ]);
    expect(optimisticIds).toHaveLength(2);
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("does not expose a normalized realtime success as retryable after its response errors", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const key = issueKeys.timeline("workspace-1", "issue-1");
    queryClient.setQueryData<TimelineQueryData>(key, { entries: [], truncatedKinds: [] });
    let rejectCreate: (error: Error) => void = () => undefined;
    mockCreateComment.mockImplementationOnce(
      () => new Promise((_resolve, reject) => { rejectCreate = reject; }),
    );
    const { result, unmount } = await renderHook(() => useCreateComment("issue-1"), {
      wrapper: wrapperFor(queryClient),
    });

    let mutation: Promise<unknown> | undefined;
    await act(async () => {
      mutation = result.current.mutateAsync({ content: "hello\u0000" });
      await Promise.resolve();
    });
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", {
      type: "comment",
      id: "comment-normalized",
      actor_type: "member",
      actor_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: "2026-09-05T09:00:00Z",
    });
    await act(async () => {
      rejectCreate(new Error("connection lost"));
      await mutation?.catch(() => undefined);
    });

    expect(mockMarkFailed).not.toHaveBeenCalled();
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-normalized",
    ]);
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });
});

describe("useEditComment content_base anchoring", () => {
  const editedComment = {
    id: "comment-1",
    issue_id: "issue-1",
    author_type: "member",
    author_id: "member-1",
    content: "edited text",
    parent_id: null,
    created_at: "2026-09-05T09:00:00Z",
    updated_at: "2026-09-05T10:00:00Z",
    type: "comment",
    reactions: [],
    attachments: [],
  };

  beforeEach(() => {
    jest.clearAllMocks();
    mockUpdateComment.mockResolvedValue(editedComment);
  });

  it("sends the pre-edit content as content_base even though onMutate already rewrote the cached content", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const key = issueKeys.timeline("workspace-1", "issue-1");
    const originalEntry: TimelineEntry = {
      type: "comment",
      id: "comment-1",
      actor_type: "member",
      actor_id: "member-1",
      content: "original text",
      parent_id: null,
      comment_type: "comment",
      reactions: [],
      attachments: [],
      created_at: "2026-09-05T09:00:00Z",
      updated_at: "2026-09-05T09:00:00Z",
    };
    queryClient.setQueryData<TimelineQueryData>(key, {
      entries: [originalEntry],
      truncatedKinds: [],
    });
    const { result, unmount } = await renderHook(
      () => useEditComment("issue-1"),
      { wrapper: wrapperFor(queryClient) },
    );

    await act(async () => {
      await result.current.mutateAsync({
        commentId: "comment-1",
        content: "edited text",
        contentBase: "original text",
      });
    });

    // CAS baseline must be the pre-edit content captured by the caller,
    // not the cached content that onMutate has already replaced.
    expect(mockUpdateComment).toHaveBeenCalledWith(
      "comment-1",
      "edited text",
      undefined,
      "original text",
    );
    // The optimistic patch did run before mutationFn — this scenario is
    // real, the cache really was rewritten before the request fired.
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(queryClient.getQueryData<TimelineQueryData>(key)).toMatchObject({
      entries: [expect.objectContaining({ type: "comment", content: "edited text" })],
    });
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });
});
