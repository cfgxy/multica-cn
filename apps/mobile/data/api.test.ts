// @vitest-environment node

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError, api } from "./api";

const { getCurrentSlug } = vi.hoisted(() => ({
  getCurrentSlug: vi.fn<() => string | null>(() => null),
}));

// RUYI-567：fetchRaw 回前台超期中止的缝线桩。api.ts 自身不 import
// react-native（保持 vitest node lane 可加载），平台层经 setOptions 注入。
const { subscribeAppState, appStateListeners } = vi.hoisted(() => {
  const listeners = new Set<(state: string) => void>();
  return {
    appStateListeners: listeners,
    subscribeAppState: (listener: (state: string) => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
});

vi.mock("i18next", () => ({
  default: { t: (_key: string, defaultValue?: string) => defaultValue ?? _key },
}));

function dispatchAppState(state: string) {
  for (const listener of [...appStateListeners]) listener(state);
}

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

describe("ApiClient.listArchivedInbox", () => {
  afterEach(() => {
    api.setToken(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // RUYI-532: the mobile archived sub-view backs onto the same capped
  // endpoint web/desktop use (packages/core/api/client.ts listArchivedInbox).
  const archivedRow = {
    id: "inbox-archived-1",
    workspace_id: "workspace-1",
    recipient_type: "member",
    recipient_id: "member-1",
    type: "new_comment",
    title: "Archived notification",
    archived: true,
  };

  it("requests the archived endpoint and returns the parsed rows", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify([archivedRow]),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(api.listArchivedInbox()).resolves.toMatchObject([
      { id: "inbox-archived-1", archived: true },
    ]);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/inbox/archived",
      expect.anything(),
    );
  });

  // Same schema-guard contract as listInbox: a contract drift must render an
  // empty archive, not take the whole screen down with it.
  it("falls back to an empty list when the response body is malformed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ items: "not-an-array" }),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    await expect(api.listArchivedInbox()).resolves.toEqual([]);
  });
});

describe("fetchRaw timeout semantics (RUYI-567)", () => {
  // whatwg-fetch 语义的桩：signal 一旦 abort 就以 AbortError 拒绝（RN 的
  // fetch 是 XHR polyfill，abort 会取消底层请求并拒绝 promise）；否则永久
  // 挂起，直到测试经 resolveWithOk 手动结算。
  let resolveFetch: ((response: unknown) => void) | null = null;
  const pendingFetch = vi.fn((_url: string, init: RequestInit = {}) => {
    const signal = init.signal as AbortSignal;
    const promise = new Promise<unknown>((resolve, reject) => {
      signal.addEventListener("abort", () => {
        const error = new Error("Aborted");
        error.name = "AbortError";
        reject(error);
      });
      resolveFetch = resolve;
    });
    promise.catch(() => {});
    return promise;
  });

  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("fetch", pendingFetch);
    api.setOptions({ subscribeAppState });
    vi.spyOn(console, "log").mockImplementation(() => undefined);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
  });

  afterEach(() => {
    resolveFetch = null;
    appStateListeners.clear();
    api.setOptions({ subscribeAppState: undefined });
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  function resolveWithOk() {
    resolveFetch?.({ ok: true, status: 200, json: async () => ({}) });
  }

  it("aborts a still-pending request on return to foreground past the deadline", async () => {
    const pending = api.sendCode("user@test.local");
    expect(appStateListeners.size).toBe(1);

    // 后台期间 RN 暂停 JS 定时器（Timing onHostPause）：把墙钟拨过
    // deadline 但不触发 timer，只有回前台（active 事件）才可能发现超期。
    vi.setSystemTime(Date.now() + 40_000);
    dispatchAppState("active");

    let settled = false;
    const observed = pending.then(
      () => {
        settled = true;
      },
      () => {
        settled = true;
      },
    );
    await vi.advanceTimersByTimeAsync(0);
    expect(settled).toBe(true);
    await expect(pending).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
    });
    void observed;
    expect(appStateListeners.size).toBe(0);
  });

  it("does not abort when returning to foreground before the deadline", async () => {
    const pending = api.sendCode("user@test.local");
    vi.setSystemTime(Date.now() + 10_000);
    dispatchAppState("active");

    resolveWithOk();
    await vi.advanceTimersByTimeAsync(0);
    await expect(pending).resolves.toBeUndefined();
    expect(appStateListeners.size).toBe(0);

    // 结算后监听已移除，超期后再派发事件必须是 no-op。
    vi.setSystemTime(Date.now() + 60_000);
    expect(() => dispatchAppState("active")).not.toThrow();
  });

  it("foreground timer still aborts at the 30s deadline (existing semantics)", async () => {
    const pending = api.sendCode("user@test.local");
    // 中止拒绝可能在断言挂接前 flush（fake timer 的 await 会冲微任务），
    // 先同步挂一个空 handler 防 unhandled rejection。
    const observed = pending.catch(() => {});
    await vi.advanceTimersByTimeAsync(30_000);
    await expect(pending).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
    });
    void observed;
    expect(appStateListeners.size).toBe(0);
  });

  it("surfaces ApiError with status 0 for a foreground-resume timeout abort", async () => {
    const pending = api.sendCode("user@test.local");
    pending.catch(() => {});
    vi.setSystemTime(Date.now() + 40_000);
    dispatchAppState("active");
    const error = await pending.then(
      () => null,
      (err: unknown) => err,
    );
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(0);
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
