// Cache-coherence tests for the mobile inbox mutations that back the
// archived sub-view (RUYI-532). The archived cache is a second, independent
// cache entry — every mutation that can move a row across the archive
// boundary or flip a row's read state inside it must keep both entries
// coherent, mirroring packages/core/inbox/mutations.ts.
import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { InboxItem } from "@multica/core/types";

const mockUnarchiveInbox = jest.fn();
const mockMarkInboxRead = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    unarchiveInbox: (...args: unknown[]) => mockUnarchiveInbox(...args),
    markInboxRead: (...args: unknown[]) => mockMarkInboxRead(...args),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1" }),
}));

import { useMarkInboxRead, useUnarchiveInbox } from "@/data/mutations/inbox";
import { inboxKeys } from "@/data/queries/inbox";

function row(overrides: Partial<InboxItem>): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "workspace-1",
    recipient_type: "member",
    recipient_id: "member-1",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "new_comment",
    severity: "info",
    issue_id: "issue-1",
    title: "Issue title",
    body: null,
    issue_status: null,
    read: false,
    archived: true,
    created_at: "2026-06-15T08:00:00Z",
    details: null,
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

function newClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

describe("useUnarchiveInbox", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockUnarchiveInbox.mockResolvedValue(row({ archived: false }));
  });

  it("flips the tapped row AND its issue siblings out of the archived cache at once", async () => {
    const queryClient = newClient();
    const archivedCache: InboxItem[] = [
      row({ id: "target" }),
      row({ id: "sibling", created_at: "2026-06-15T07:00:00Z" }),
      row({ id: "other-issue", issue_id: "issue-2" }),
    ];
    queryClient.setQueryData(inboxKeys.archived("workspace-1"), archivedCache);
    const invalidateSpy = jest.spyOn(queryClient, "invalidateQueries");

    const { result, unmount } = await renderHook(() => useUnarchiveInbox(), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync("target");
    });

    const archived = queryClient.getQueryData<InboxItem[]>(
      inboxKeys.archived("workspace-1"),
    );
    expect(archived?.find((i) => i.id === "target")?.archived).toBe(false);
    // The server unarchives the whole issue group — patching only the tapped
    // row would let the sibling linger in the archived list until the refetch.
    expect(archived?.find((i) => i.id === "sibling")?.archived).toBe(false);
    expect(archived?.find((i) => i.id === "other-issue")?.archived).toBe(true);

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const invalidatedKeys = invalidateSpy.mock.calls.map(
      (call) => (call[0] as { queryKey: readonly unknown[] })?.queryKey,
    );
    expect(invalidatedKeys).toContainEqual(inboxKeys.all("workspace-1"));
    expect(invalidatedKeys).toContainEqual(inboxKeys.unreadSummary());

    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });

  it("restores the archived cache when the API call fails", async () => {
    mockUnarchiveInbox.mockRejectedValue(new Error("network down"));
    const queryClient = newClient();
    const archivedCache: InboxItem[] = [row({ id: "target" })];
    queryClient.setQueryData(inboxKeys.archived("workspace-1"), archivedCache);

    const { result, unmount } = await renderHook(() => useUnarchiveInbox(), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync("target").catch(() => undefined);
    });

    expect(queryClient.getQueryData<InboxItem[]>(inboxKeys.archived("workspace-1"))).toEqual(
      archivedCache,
    );
    // Mutation state propagates to the observer on a batched notification —
    // poll instead of asserting synchronously after mutateAsync resolves.
    await waitFor(() => expect(result.current.isError).toBe(true));

    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });
});

describe("useMarkInboxRead", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockMarkInboxRead.mockResolvedValue(row({ read: true }));
  });

  // Opening a notification from the archived sub-view marks it read too —
  // patching only the list cache would leave the unread dot sitting in the
  // archived list until the next refetch.
  it("flips the read state in both the list and the archived cache", async () => {
    const queryClient = newClient();
    queryClient.setQueryData(inboxKeys.list("workspace-1"), [
      row({ id: "target", archived: false, read: false }),
    ]);
    queryClient.setQueryData(inboxKeys.archived("workspace-1"), [
      row({ id: "target", read: false }),
    ]);

    const { result, unmount } = await renderHook(() => useMarkInboxRead(), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync("target");
    });

    const list = queryClient.getQueryData<InboxItem[]>(inboxKeys.list("workspace-1"));
    const archived = queryClient.getQueryData<InboxItem[]>(
      inboxKeys.archived("workspace-1"),
    );
    expect(list?.[0]?.read).toBe(true);
    expect(archived?.[0]?.read).toBe(true);

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });
});
