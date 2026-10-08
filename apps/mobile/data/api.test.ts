// @vitest-environment node

import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, api } from "./api";

const { getCurrentSlug } = vi.hoisted(() => ({
  getCurrentSlug: vi.fn<() => string | null>(() => null),
}));

vi.mock("@/data/server-store", () => ({
  getApiUrl: () => "https://api.example.test",
}));

vi.mock("@/data/workspace-store", () => ({
  getCurrentSlug,
}));

vi.mock("@/lib/request-id", () => ({
  createRequestId: () => "request-1",
}));

const timelineEntry = {
  type: "activity",
  id: "activity-1",
  actor_type: "member",
  actor_id: "member-1",
  created_at: "2026-09-05T09:00:00Z",
};

describe("ApiClient.listAgents", () => {
  afterEach(() => {
    api.setToken(null);
    getCurrentSlug.mockReturnValue(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // Scope counts and the archived segment are computed client-side from this
  // single list (web parity: packages/core/workspace/queries.ts:75 passes
  // include_archived: true unconditionally) — without it the archived scope
  // is permanently empty (RUYI-346 defect #1).
  it("always requests archived agents so scope filters work client-side", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify([]),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(api.listAgents()).resolves.toEqual([]);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/agents?include_archived=true",
      expect.anything(),
    );
  });

  // RUYI-463 P1: share-target fetches agents for the PICKED workspace while
  // the current-workspace mirror may be empty (fresh user, never opened any
  // workspace) or point at a different one (active A, picked B). The explicit
  // slug must ride the wire so the request never depends on the mirror.
  it("pins X-Workspace-Slug from the explicit workspaceSlug when the mirror is empty", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify([]),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(
      api.listAgents({ workspaceSlug: "picked-ws" }),
    ).resolves.toEqual([]);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/agents?include_archived=true",
      expect.objectContaining({
        headers: expect.objectContaining({ "X-Workspace-Slug": "picked-ws" }),
      }),
    );
  });

  it("lets the explicit workspaceSlug win over the mirror-injected one", async () => {
    getCurrentSlug.mockReturnValue("active-ws");
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify([]),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(
      api.listAgents({ workspaceSlug: "picked-ws" }),
    ).resolves.toEqual([]);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/agents?include_archived=true",
      expect.objectContaining({
        headers: expect.objectContaining({ "X-Workspace-Slug": "picked-ws" }),
      }),
    );
  });

  it("keeps the mirror-injected slug when no explicit workspace is pinned", async () => {
    getCurrentSlug.mockReturnValue("active-ws");
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify([]),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(api.listAgents()).resolves.toEqual([]);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/agents?include_archived=true",
      expect.objectContaining({
        headers: expect.objectContaining({ "X-Workspace-Slug": "active-ws" }),
      }),
    );
  });
});

describe("ApiClient.listTimeline", () => {
  afterEach(() => {
    api.setToken(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("returns the parsed entries with normalized truncation metadata", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify([timelineEntry]),
      { headers: { "X-Timeline-Truncated": " activity, comment, activity " } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(api.listTimeline("issue-1")).resolves.toEqual({
      entries: [timelineEntry],
      truncatedKinds: ["activity", "comment"],
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/issues/issue-1/timeline",
      expect.objectContaining({
        headers: expect.objectContaining({ "X-Request-ID": "request-1" }),
      }),
    );
  });

  it("keeps a valid truncation header when the response body is malformed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ entries: "not-an-array" }),
      { headers: { "X-Timeline-Truncated": "activity" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    await expect(api.listTimeline("issue-1")).resolves.toEqual({
      entries: [],
      truncatedKinds: ["activity"],
    });
  });
});

describe("ApiClient.uploadFile", () => {
  afterEach(() => {
    api.setToken(null);
    api.setOptions({ onUnauthorized: undefined });
    getCurrentSlug.mockReturnValue(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  const pngAsset = {
    uri: "file:///tmp/a.png",
    name: "a.png",
    type: "image/png",
  };

  // RN 的 fetch 在 signal 中止时以 name 为 "AbortError" 的错误 reject——
  // 模拟这种「悬挂到 signal 才 settle」的形状，正是 RUYI-569 修复前
  // uploadFile 在弱网下的表现。
  const makeHangingFetch = () =>
    vi.fn(
      (_path: string, init: RequestInit): Promise<Response> =>
        new Promise((_, reject) => {
          init.signal?.addEventListener("abort", () =>
            reject(Object.assign(new Error("Aborted"), { name: "AbortError" })),
          );
        }),
    );

  it("uploads multipart without a preset Content-Type and releases the timeout timer on success", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({
        id: "att-1",
        filename: "a.png",
        url: "https://cdn.example.test/a.png",
      }),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    const attachment = await api.uploadFile(pngAsset, { issueId: "issue-1" });

    expect(attachment).toMatchObject({
      id: "att-1",
      filename: "a.png",
      url: "https://cdn.example.test/a.png",
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("POST");
    // multipart 边界必须由 fetch 自己设置，不能预设 Content-Type。
    expect(init.headers && !("Content-Type" in init.headers)).toBe(true);
    expect((init.headers as Record<string, string>)["X-Request-ID"]).toBe(
      "request-1",
    );
    expect(init.body).toBeInstanceOf(FormData);
    expect(init.signal).toBeInstanceOf(AbortSignal);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("fails with a distinguishable status-0 ApiError when the upload exceeds 120s", async () => {
    vi.useFakeTimers();
    const fetchMock = makeHangingFetch();
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const pending = api.uploadFile(pngAsset);
    // 先同步挂上 rejection handler 再推进 fake timers，否则 reject 发生在
    // advanceTimersByTimeAsync 内部，Node 会记一次 unhandled rejection。
    const settled = pending.then(
      () => {
        throw new Error("upload should have timed out");
      },
      (e: unknown) => e,
    );
    await vi.advanceTimersByTimeAsync(120_000);

    const err: unknown = await settled;
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(0);
    expect((err as Error).message).toContain("timed out");
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.signal?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("propagates caller cancellation as-is instead of classifying it as a timeout", async () => {
    vi.useFakeTimers();
    const fetchMock = makeHangingFetch();
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    const caller = new AbortController();
    const pending = api.uploadFile(pngAsset, { signal: caller.signal });
    const settled = pending.then(
      () => {
        throw new Error("upload should have been cancelled");
      },
      (e: unknown) => e,
    );
    caller.abort();

    const err: unknown = await settled;
    // 调用方取消原样透传（与 fetchRaw 对 caller abort 的处理一致），
    // 不得误分类为超时 ApiError。
    expect(err).not.toBeInstanceOf(ApiError);
    expect((err as Error).name).toBe("AbortError");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("maps HTTP failures to ApiError and keeps the 401 sign-out hook", async () => {
    const onUnauthorized = vi.fn();
    api.setOptions({ onUnauthorized });
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ message: "unauthorized" }),
      { status: 401, headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);
    vi.spyOn(console, "error").mockImplementation(() => undefined);

    const err: unknown = await api.uploadFile(pngAsset).then(
      () => {
        throw new Error("upload should have failed");
      },
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    expect((err as ApiError).message).toBe("unauthorized");
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });
});
