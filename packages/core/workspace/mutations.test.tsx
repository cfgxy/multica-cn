/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { defaultStorage } from "../platform/storage";
import type { Workspace } from "../types";
import {
  useCreateWorkspace,
  useDeleteWorkspace,
  usePublishMarketplaceListing,
  useWithdrawMarketplaceListing,
} from "./mutations";
import { workspaceKeys } from "./queries";
import {
  isWorkspaceDeletePending,
  unmarkWorkspaceDeletePending,
} from "./pending-delete";

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

const makeWorkspace = (id: string, slug: string): Workspace => ({
  id,
  name: slug,
  slug,
  description: null,
  context: null,
  settings: {},
  repos: [],
  issue_prefix: "MUL",
  avatar_url: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
});

describe("useCreateWorkspace", () => {
  let qc: QueryClient;
  let createWorkspace: ReturnType<
    typeof vi.fn<(data: { name: string; slug: string }) => Promise<Workspace>>
  >;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    createWorkspace = vi.fn();
    setApiInstance({ createWorkspace } as unknown as ApiClient);
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      makeWorkspace("ws-1", "existing"),
    ]);
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("seeds the successful response without invalidating the workspace list", async () => {
    const created = makeWorkspace("ws-2", "created");
    createWorkspace.mockResolvedValue(created);
    const { result } = renderHook(() => useCreateWorkspace(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync({ name: "Created", slug: "created" });
    });

    expect(
      qc
        .getQueryData<Workspace[]>(workspaceKeys.list())
        ?.map((workspace) => workspace.id),
    ).toEqual(["ws-1", "ws-2"]);
    expect(qc.getQueryState(workspaceKeys.list())?.isInvalidated).toBe(false);
  });

  it("invalidates the workspace list when create fails", async () => {
    createWorkspace.mockRejectedValue(new Error("response lost"));
    const { result } = renderHook(() => useCreateWorkspace(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await expect(
        result.current.mutateAsync({ name: "Created", slug: "created" }),
      ).rejects.toThrow("response lost");
    });

    expect(qc.getQueryState(workspaceKeys.list())?.isInvalidated).toBe(true);
    expect(
      qc
        .getQueryData<Workspace[]>(workspaceKeys.list())
        ?.map((workspace) => workspace.id),
    ).toEqual(["ws-1"]);
  });
});

describe("useDeleteWorkspace", () => {
  let qc: QueryClient;
  let deleteWorkspace: ReturnType<typeof vi.fn<(id: string) => Promise<void>>>;
  let listWorkspaces: ReturnType<typeof vi.fn<() => Promise<Workspace[]>>>;

  const serverList = () => [
    makeWorkspace("ws-1", "keep-me"),
    makeWorkspace("ws-2", "delete-me"),
  ];

  const seedList = () => {
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), serverList());
  };

  const cachedList = () =>
    qc.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    deleteWorkspace = vi.fn().mockResolvedValue(undefined);
    listWorkspaces = vi.fn().mockResolvedValue(serverList());
    setApiInstance({ deleteWorkspace, listWorkspaces } as unknown as ApiClient);
  });

  afterEach(() => {
    qc.clear();
    // The self-initiated marker is module state and is intentionally KEPT
    // after a successful delete (it suppresses the WS echo); reset it so
    // tests stay independent.
    unmarkWorkspaceDeletePending("ws-2");
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("leaves the list cache untouched while the DELETE is pending (no optimistic removal)", async () => {
    seedList();
    // Hold the DELETE open to observe the pending window. The flow awaits
    // the mutation with the dialog in a loading state, so the cache must
    // keep reflecting server truth: the workspace still exists.
    let resolveDelete!: () => void;
    deleteWorkspace.mockReturnValue(
      new Promise<void>((resolve) => {
        resolveDelete = resolve;
      }),
    );

    const { result } = renderHook(() => useDeleteWorkspace(), {
      wrapper: createWrapper(qc),
    });

    let mutationDone: Promise<void>;
    await act(async () => {
      mutationDone = result.current.mutateAsync("ws-2");
      await Promise.resolve();
    });

    expect(deleteWorkspace).toHaveBeenCalledWith("ws-2");
    expect(cachedList().map((w) => w.id)).toEqual(["ws-1", "ws-2"]);

    await act(async () => {
      resolveDelete();
      await mutationDone;
    });
  });

  it("invalidates the workspace list after a successful delete", async () => {
    seedList();
    const { result } = renderHook(() => useDeleteWorkspace(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync("ws-2");
    });

    expect(qc.getQueryState(workspaceKeys.list())?.isInvalidated).toBe(true);
  });

  it("clears the deleted slug's workspace-scoped storage on success", async () => {
    seedList();
    // The realtime `workspace:deleted` handler skips self-initiated deletes,
    // so the mutation owns this cleanup; the slug is captured from the list
    // cache before the mutation fires.
    defaultStorage.setItem("multica_issue_draft:delete-me", "draft");
    defaultStorage.setItem("multica_issue_draft:keep-me", "draft");

    const { result } = renderHook(() => useDeleteWorkspace(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync("ws-2");
    });

    expect(defaultStorage.getItem("multica_issue_draft:delete-me")).toBeNull();
    expect(defaultStorage.getItem("multica_issue_draft:keep-me")).toBe("draft");
  });

  it("leaves storage and cache untouched when the DELETE fails", async () => {
    seedList();
    deleteWorkspace.mockRejectedValue(new Error("boom"));
    defaultStorage.setItem("multica_issue_draft:delete-me", "draft");

    const { result } = renderHook(() => useDeleteWorkspace(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await expect(result.current.mutateAsync("ws-2")).rejects.toThrow("boom");
    });

    // No optimistic write happened, so there is nothing to roll back.
    expect(defaultStorage.getItem("multica_issue_draft:delete-me")).toBe("draft");
    expect(cachedList().map((w) => w.id)).toEqual(["ws-1", "ws-2"]);
  });

  it("keeps the self-initiated marker after success and lifts it after failure", async () => {
    seedList();
    const { result } = renderHook(() => useDeleteWorkspace(), {
      wrapper: createWrapper(qc),
    });

    // Success: the id is gone for good; the kept marker suppresses the WS
    // echo of our own delete whenever it arrives.
    await act(async () => {
      await result.current.mutateAsync("ws-2");
    });
    expect(isWorkspaceDeletePending("ws-2")).toBe(true);

    // Failure: the workspace still exists, so a later external delete of
    // the same id must be handled by the realtime handler again.
    unmarkWorkspaceDeletePending("ws-2");
    deleteWorkspace.mockRejectedValue(new Error("boom"));
    await act(async () => {
      await expect(result.current.mutateAsync("ws-2")).rejects.toThrow("boom");
    });
    expect(isWorkspaceDeletePending("ws-2")).toBe(false);
  });
});

describe("marketplace listing cache invalidation", () => {
  let qc: QueryClient;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  // The catalog is global. A workspace the user visited earlier is still
  // holding the pre-publish copy of it, so publishing from one workspace has
  // to reach every cached catalog, not just the publisher's.
  const seedCatalogs = () => {
    qc.setQueryData(workspaceKeys.marketplace("ws-1", "mcp", ""), []);
    qc.setQueryData(workspaceKeys.marketplace("ws-2", "mcp", ""), []);
    qc.setQueryData(workspaceKeys.marketplaceListings("ws-1"), []);
    qc.setQueryData(workspaceKeys.marketplaceListings("ws-2"), []);
  };

  const invalidated = (key: readonly unknown[]) =>
    qc.getQueryState(key)?.isInvalidated === true;

  it("invalidates every workspace's cached catalog on publish", async () => {
    seedCatalogs();
    const publishMarketplaceListing = vi.fn().mockResolvedValue({});
    setApiInstance({ publishMarketplaceListing } as unknown as ApiClient);

    const { result } = renderHook(() => usePublishMarketplaceListing("ws-1"), {
      wrapper: createWrapper(qc),
    });
    await act(async () => {
      await result.current.mutateAsync({ kind: "mcp", name: "acme" });
    });

    expect(invalidated(workspaceKeys.marketplace("ws-1", "mcp", ""))).toBe(true);
    expect(invalidated(workspaceKeys.marketplace("ws-2", "mcp", ""))).toBe(true);
  });

  it("invalidates every workspace's cached catalog on withdraw", async () => {
    seedCatalogs();
    const withdrawMarketplaceListing = vi.fn().mockResolvedValue({});
    setApiInstance({ withdrawMarketplaceListing } as unknown as ApiClient);

    const { result } = renderHook(() => useWithdrawMarketplaceListing("ws-1"), {
      wrapper: createWrapper(qc),
    });
    await act(async () => {
      await result.current.mutateAsync({ id: "listing-1", revision: 3 });
    });

    expect(invalidated(workspaceKeys.marketplace("ws-1", "mcp", ""))).toBe(true);
    expect(invalidated(workspaceKeys.marketplace("ws-2", "mcp", ""))).toBe(true);
  });

  it("leaves another workspace's management list alone", async () => {
    seedCatalogs();
    const publishMarketplaceListing = vi.fn().mockResolvedValue({});
    setApiInstance({ publishMarketplaceListing } as unknown as ApiClient);

    const { result } = renderHook(() => usePublishMarketplaceListing("ws-1"), {
      wrapper: createWrapper(qc),
    });
    await act(async () => {
      await result.current.mutateAsync({ kind: "mcp", name: "acme" });
    });

    // Only ws-1 published; ws-2's own listings did not change.
    expect(invalidated(workspaceKeys.marketplaceListings("ws-1"))).toBe(true);
    expect(invalidated(workspaceKeys.marketplaceListings("ws-2"))).toBe(false);
  });
});
