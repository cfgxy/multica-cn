import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TimelineQueryData } from "@multica/core/issues/timeline-query";
import type { TimelineEntry } from "@multica/core/types";

const mockCreateComment = jest.fn();
const mockUpdateComment = jest.fn();
const mockMarkFailed = jest.fn();
const mockFailedComments: Record<string, unknown> = {};

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
    getState: () => ({
      failed: mockFailedComments,
      markFailed: (id: string, payload: unknown) => {
        mockFailedComments[id] = payload;
        mockMarkFailed(id, payload);
      },
      clear: (id: string) => { delete mockFailedComments[id]; },
    }),
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
    Object.keys(mockFailedComments).forEach((id) => delete mockFailedComments[id]);
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
    expect(Object.keys(mockFailedComments)).toHaveLength(0);
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

    expect(Object.values(mockFailedComments)).toEqual([
      expect.objectContaining({ content: "hello", error: "offline" }),
    ]);
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-saved",
      expect.stringMatching(/^optimistic-/),
    ]);
    expect(optimisticIds).toHaveLength(2);
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("keeps the failed request retryable when another identical request confirms the staged event id", async () => {
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
      id: "comment-saved",
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
        id: "comment-saved",
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
    expect(mockMarkFailed).toHaveBeenCalledWith(
      optimisticIds[0],
      expect.objectContaining({ content: "hello", error: "offline" }),
    );
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-saved",
      optimisticIds[0],
    ]);
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("keeps the real failure when the same-id response arrives before the other request errors", async () => {
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
    const commentEvent = {
      type: "comment" as const,
      id: "comment-saved",
      actor_type: "member" as const,
      actor_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: "2026-09-05T09:00:00Z",
    };
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", commentEvent);

    await act(async () => {
      resolveSecond({
        id: "comment-saved",
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
    await act(async () => {
      rejectFirst(new Error("offline"));
      await first?.catch(() => undefined);
    });

    expect(Object.keys(mockFailedComments)).toEqual([optimisticIds[0]]);
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-saved",
      optimisticIds[0],
    ]);
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("keeps an earlier failed send when another identical send emits its event then response", async () => {
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
      first = result.current.mutateAsync({
        content: "hello",
        parentId: "parent-1",
        attachmentIds: ["attachment-1"],
      });
      second = result.current.mutateAsync({ content: "hello", parentId: "parent-1" });
      await Promise.resolve();
    });
    const optimisticIds = queryClient
      .getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id) ?? [];

    await act(async () => {
      rejectFirst(new Error("offline"));
      await first?.catch(() => undefined);
    });
    expect(Object.keys(mockFailedComments)).toEqual([optimisticIds[0]]);

    const commentEvent = {
      type: "comment" as const,
      id: "comment-saved",
      actor_type: "member" as const,
      actor_id: "member-1",
      content: "hello",
      parent_id: "parent-1",
      created_at: "2026-09-05T09:00:00Z",
    };
    // Match the production realtime callback's failedIds argument.
    reconcileCreatedComment(
      queryClient,
      "workspace-1",
      "issue-1",
      commentEvent,
      undefined,
      new Set(Object.keys(mockFailedComments)),
    );

    await act(async () => {
      resolveSecond({
        id: "comment-saved",
        issue_id: "issue-1",
        author_type: "member",
        author_id: "member-1",
        content: "hello",
        parent_id: "parent-1",
        created_at: "2026-09-05T09:00:00Z",
        updated_at: "2026-09-05T09:00:00Z",
        type: "comment",
        reactions: [],
        attachments: [],
      });
      await second;
    });

    expect(Object.keys(mockFailedComments)).toEqual([optimisticIds[0]]);
    expect(mockFailedComments[optimisticIds[0]]).toEqual(expect.objectContaining({
      content: "hello",
      parentId: "parent-1",
      attachmentIds: ["attachment-1"],
      error: "offline",
    }));
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: optimisticIds[0], content: "hello", parent_id: "parent-1" }),
      expect.objectContaining({ id: "comment-saved", content: "hello", parent_id: "parent-1" }),
    ]));
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("keeps two comments when the same event id is delivered twice before both requests fail", async () => {
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
    const commentEvent = {
      type: "comment" as const,
      id: "comment-a",
      actor_type: "member" as const,
      actor_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: "2026-09-05T09:00:00Z",
    };
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", commentEvent);
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", commentEvent);
    reconcileCreatedComment(queryClient, "workspace-1", "issue-1", {
      ...commentEvent,
      id: "comment-b",
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

  it("reconciles events that arrive after all matching requests have failed", async () => {
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

    await act(async () => {
      rejectFirst(new Error("connection lost"));
      await first?.catch(() => undefined);
      rejectSecond(new Error("connection lost"));
      await second?.catch(() => undefined);
    });
    expect(Object.keys(mockFailedComments)).toEqual(optimisticIds);

    const commentEvent = (id: string, secondOffset = 0) => ({
      type: "comment" as const,
      id,
      actor_type: "member" as const,
      actor_id: "member-1",
      content: "hello",
      parent_id: null,
      created_at: `2026-09-05T09:00:0${secondOffset}Z`,
    });
    const reconcileRealtimeEvent = (id: string, secondOffset = 0) => {
      const reconciledIds = reconcileCreatedComment(
        queryClient,
        "workspace-1",
        "issue-1",
        commentEvent(id, secondOffset),
        undefined,
        new Set(Object.keys(mockFailedComments)),
      );
      reconciledIds.forEach((failedId) => { delete mockFailedComments[failedId]; });
    };

    reconcileRealtimeEvent("comment-a");
    reconcileRealtimeEvent("comment-a");
    reconcileRealtimeEvent("comment-b", 1);

    expect(Object.keys(mockFailedComments)).toEqual([]);
    expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-a",
      "comment-b",
    ]);
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it.each([true, false])(
    "drains every staged success when the final matching request fails (first event before error: %s)",
    async (firstEventBeforeError) => {
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
      const commentEvent = (id: string, secondOffset = 0) => ({
        type: "comment" as const,
        id,
        actor_type: "member" as const,
        actor_id: "member-1",
        content: "hello",
        parent_id: null,
        created_at: `2026-09-05T09:00:0${secondOffset}Z`,
      });
      const emit = (id: string, secondOffset = 0) => {
        const reconciledIds = reconcileCreatedComment(
          queryClient,
          "workspace-1",
          "issue-1",
          commentEvent(id, secondOffset),
          undefined,
          new Set(Object.keys(mockFailedComments)),
        );
        reconciledIds.forEach((failedId) => { delete mockFailedComments[failedId]; });
      };

      if (firstEventBeforeError) emit("comment-a");
      await act(async () => {
        rejectFirst(new Error("connection lost"));
        await first?.catch(() => undefined);
      });
      if (!firstEventBeforeError) emit("comment-a");
      emit("comment-b", 1);
      await act(async () => {
        rejectSecond(new Error("connection lost"));
        await second?.catch(() => undefined);
      });

      expect(Object.keys(mockFailedComments)).toEqual([]);
      expect(queryClient.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
        "comment-a",
        "comment-b",
      ]);
      expect(queryClient.getQueryData<TimelineQueryData & { observedCreatedComments?: TimelineEntry[] }>(key)
        ?.observedCreatedComments).toBeUndefined();
      await unmount();
      queryClient.clear();
      queryClient.unmount();
    },
  );

  it("keeps one failed payload when only one late event follows two request errors", async () => {
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
      first = result.current.mutateAsync({ content: "hello", parentId: "parent-1" });
      second = result.current.mutateAsync({
        content: "hello",
        parentId: "parent-1",
        attachmentIds: ["attachment-2"],
      });
      await Promise.resolve();
    });
    const optimisticIds = queryClient
      .getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id) ?? [];

    await act(async () => {
      rejectFirst(new Error("connection lost"));
      await first?.catch(() => undefined);
      rejectSecond(new Error("connection lost"));
      await second?.catch(() => undefined);
    });
    const reconciledIds = reconcileCreatedComment(
      queryClient,
      "workspace-1",
      "issue-1",
      {
        type: "comment",
        id: "comment-saved",
        actor_type: "member",
        actor_id: "member-1",
        content: "hello",
        parent_id: "parent-1",
        created_at: "2026-09-05T09:00:00Z",
      },
      undefined,
      new Set(Object.keys(mockFailedComments)),
    );
    reconciledIds.forEach((failedId) => { delete mockFailedComments[failedId]; });

    const failedIds = Object.keys(mockFailedComments);
    expect(failedIds).toHaveLength(1);
    expect(failedIds[0]).toBe(optimisticIds[1]);
    expect(mockFailedComments[failedIds[0]]).toEqual(expect.objectContaining({
      content: "hello",
      parentId: "parent-1",
      attachmentIds: ["attachment-2"],
      error: "connection lost",
    }));
    const entries = queryClient.getQueryData<TimelineQueryData>(key)?.entries ?? [];
    expect(entries.map(({ id }) => id)).toEqual(["comment-saved", optimisticIds[1]]);
    expect(entries[1]).toMatchObject({ content: "hello", parent_id: "parent-1" });
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
