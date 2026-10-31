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
    api.setOptions({ subscribeAppState: undefined, onUnauthorized: undefined });
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
    // RUYI-568：send-code 的 deadline 已收紧到 10s，回前台重判沿用同一
    // per-request 值——这里停在 deadline 之前（5s）以验证"未超期不中止"。
    vi.setSystemTime(Date.now() + 5_000);
    dispatchAppState("active");

    resolveWithOk();
    await vi.advanceTimersByTimeAsync(0);
    await expect(pending).resolves.toBeUndefined();
    expect(appStateListeners.size).toBe(0);

    // 结算后监听已移除，超期后再派发事件必须是 no-op。
    vi.setSystemTime(Date.now() + 60_000);
    expect(() => dispatchAppState("active")).not.toThrow();
  });

  it("generic endpoints still abort at the 30s ceiling (existing semantics)", async () => {
    // RUYI-568：登录三链路收紧到 10s 后，通用 API 必须维持 30s 天花板——
    // 这里用 listAgents（非登录链路）锁住该语义，防止收紧被无意扩散。
    const pending = api.listAgents();
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

  it("settles deleteRuntime at 30s when React Native fetch ignores abort, then ignores a late 401", async () => {
    const lateResponse = {
      resolve: null as ((response: Response) => void) | null,
    };
    const onUnauthorized = vi.fn();
    const stubbornFetch = vi.fn(
      (_url: string, init: RequestInit): Promise<Response> =>
        new Promise((resolve) => {
          lateResponse.resolve = resolve;
          init.signal?.addEventListener("abort", () => undefined);
        }),
    );
    api.setOptions({ onUnauthorized });
    vi.stubGlobal("fetch", stubbornFetch);

    const pending = api.deleteRuntime("runtime-1");
    const observed = pending.catch(() => undefined);
    expect(appStateListeners.size).toBe(1);

    await vi.advanceTimersByTimeAsync(30_000);

    await expect(pending).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
    });
    void observed;
    const requestInit = stubbornFetch.mock.calls[0][1] as RequestInit;
    expect(requestInit.signal?.aborted).toBe(true);
    expect(appStateListeners.size).toBe(0);
    expect(vi.getTimerCount()).toBe(0);

    if (!lateResponse.resolve) throw new Error("fetch resolver was not captured");
    lateResponse.resolve(new Response(null, { status: 401 }));
    await Promise.resolve();
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("rejects an already-aborted deleteRuntime signal without starting fetch", async () => {
    const caller = new AbortController();
    const fetchMock = vi.fn();
    caller.abort();
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      api.deleteRuntime("runtime-1", { signal: caller.signal }),
    ).rejects.toMatchObject({ name: "AbortError" });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(appStateListeners.size).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("keeps caller cancellation classified as AbortError when it wins the race", async () => {
    const caller = new AbortController();
    const stubbornFetch = vi.fn(
      (_url: string, init: RequestInit): Promise<Response> =>
        new Promise(() => {
          init.signal?.addEventListener("abort", () => undefined);
        }),
    );
    vi.stubGlobal("fetch", stubbornFetch);

    const pending = api.deleteRuntime("runtime-1", { signal: caller.signal });
    const observed = pending.catch(() => undefined);
    caller.abort();
    await vi.advanceTimersByTimeAsync(30_000);

    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    void observed;
    expect(appStateListeners.size).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
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

describe("login chain 10s deadline (RUYI-568)", () => {
  // 与 RUYI-567 块同一 whatwg-fetch 语义桩：signal abort → AbortError 拒绝，
  // 否则永久挂起直到测试手动结算。本地独立一份，保持 567 块不动。
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

  it("sendCode aborts at the 10s login deadline with a per-call timeout message", async () => {
    const pending = api.sendCode("user@test.local");
    const observed = pending.catch(() => {});
    await vi.advanceTimersByTimeAsync(10_000);
    const error = await pending.then(
      () => null,
      (err: unknown) => err,
    );
    void observed;
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(0);
    expect((error as ApiError).message).toContain("10000ms");
    expect(appStateListeners.size).toBe(0);
  });

  it("verifyCode aborts at the 10s login deadline", async () => {
    const pending = api.verifyCode("user@test.local", "123456");
    const observed = pending.catch(() => {});
    await vi.advanceTimersByTimeAsync(10_000);
    await expect(pending).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
    });
    void observed;
    expect(appStateListeners.size).toBe(0);
  });

  it("getMe aborts at the 10s login deadline", async () => {
    const pending = api.getMe();
    const observed = pending.catch(() => {});
    await vi.advanceTimersByTimeAsync(10_000);
    await expect(pending).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
    });
    void observed;
    expect(appStateListeners.size).toBe(0);
  });

  it("generic endpoints are not tightened — listAgents survives past 10s", async () => {
    const pending = api.listAgents();
    // 10s 处通用链路必须仍然在途（未收紧扩散到非登录请求）。
    await vi.advanceTimersByTimeAsync(10_000);
    resolveWithOk();
    await vi.advanceTimersByTimeAsync(0);
    await expect(pending).resolves.toEqual([]);
  });

  it("caller signal cancels sendCode and cleans up the AppState listener", async () => {
    const controller = new AbortController();
    const pending = api.sendCode("user@test.local", {
      signal: controller.signal,
    });
    expect(appStateListeners.size).toBe(1);
    controller.abort();
    // 调用方取消透传原始 AbortError（区别于超时的 ApiError），登录屏以此
    // 区分「用户改邮箱主动取消」与「超时失败」。
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(appStateListeners.size).toBe(0);
  });

  it("caller signal cancels verifyCode", async () => {
    const controller = new AbortController();
    const pending = api.verifyCode("user@test.local", "123456", {
      signal: controller.signal,
    });
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(appStateListeners.size).toBe(0);
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
        new Promise(() => {
          init.signal?.addEventListener("abort", () => undefined);
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

// RUYI-576：X-Client-OS 必须来自平台运行时判定（平台层经 setOptions 注入
// Platform.OS），不得硬编码 "ios"——Android 设备流量曾被服务端记为 iOS。
// api.ts 自身不 import react-native（保持 vitest node lane 可加载），
// 平台取值沿 RUYI-567 的 setOptions 缝线注入。
describe("X-Client-OS header", () => {
  afterEach(() => {
    api.setToken(null);
    api.setOptions({ clientOS: undefined });
    getCurrentSlug.mockReturnValue(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  const pngAsset = {
    uri: "file:///tmp/a.png",
    name: "a.png",
    type: "image/png",
  };

  async function captureJsonRequestHeaderOS(): Promise<string> {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify([]),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(api.listAgents()).resolves.toEqual([]);

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    return (init.headers as Record<string, string>)["X-Client-OS"];
  }

  it("reports the injected android platform on JSON requests", async () => {
    api.setOptions({ clientOS: "android" });

    await expect(captureJsonRequestHeaderOS()).resolves.toBe("android");
  });

  it("reports the injected ios platform on JSON requests", async () => {
    api.setOptions({ clientOS: "ios" });

    await expect(captureJsonRequestHeaderOS()).resolves.toBe("ios");
  });

  it("reports the injected android platform on multipart uploads", async () => {
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
    api.setOptions({ clientOS: "android" });

    await expect(api.uploadFile(pngAsset, { issueId: "issue-1" }))
      .resolves.toMatchObject({ id: "att-1" });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect((init.headers as Record<string, string>)["X-Client-OS"]).toBe(
      "android",
    );
  });

  it("falls back to ios when the platform layer injects nothing", async () => {
    // 未注入时保持既有观测语义（旧行为），仅真实 App 的平台层注入 Platform.OS。
    await expect(captureJsonRequestHeaderOS()).resolves.toBe("ios");
  });
});

describe("api.listIssueSuspensions", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

describe("ApiClient.lookupPullRequest", () => {
  afterEach(() => {
    api.setToken(null);
    getCurrentSlug.mockReturnValue(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("unwraps the wrapped {suspensions} payload and returns the rows", async () => {
    const row = {
      id: "susp-1",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      status: "active",
      reason: "waiting for CI",
      waiting_on_type: "external",
      waiting_on_summary: "ci run 42",
      resume_condition: "ci green",
      suspended_by_type: "agent",
      suspended_by_id: "agent-1",
      suspended_at: "2026-10-14T00:00:00Z",
      resume_note: "",
      cancel_note: "",
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ suspensions: [row] }),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(api.listIssueSuspensions("issue-1")).resolves.toEqual([row]);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/issues/issue-1/suspensions",
      expect.anything(),
    );
  });
});

  // RUYI-635 返工：端点注册在 /api/workspaces/{id} 路由组内
  // （server/cmd/server/router.go），core 同口径见
  // packages/core/api/client.ts lookupPullRequest。路径缺 workspaces 段时
  // 请求命中不存在的顶层路由 → 404 → 被「404→null」降级契约吞掉 →
  // PR 引用永远静默降级为纯文本。此测试锚定请求 URL 本身，防止两端
  // 端点口径再漂移（单测 mock 掉 api 模块时结构性拦不住这类接缝缺陷）。
  it("requests the workspace-scoped lookup route", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ pull_request: null }),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(
      api.lookupPullRequest("ws-1", "cfgxy", "multica-cn", 350),
    ).resolves.toBeNull();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      "https://api.example.test/api/workspaces/ws-1/pull-requests/lookup?owner=cfgxy&repo=multica-cn&number=350",
    );
  });
});

describe("ApiClient.lookupPullRequestByNumber", () => {
  afterEach(() => {
    api.setToken(null);
    getCurrentSlug.mockReturnValue(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // RUYI-728: 正文 `MR !N` 点击的按编号解析端点，注册在
  // /api/workspaces/{id}/pull-requests/lookup-by-number
  // （server/cmd/server/router.go）。契约与 web 的
  // packages/core/api/client.ts lookupPullRequestByNumber 同口径：唯一命中
  // 返回 PR；从未同步（404）与跨仓歧义（409）都降级为 null，正文点击与
  // 引用区行同样静默降级，不报错。
  it("requests the by-number lookup route", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ pull_request: null }),
      { headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(
      api.lookupPullRequestByNumber("ws-1", 468),
    ).resolves.toBeNull();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      "https://api.example.test/api/workspaces/ws-1/pull-requests/lookup-by-number?number=468",
    );
  });

  it("degrades a 404 miss to null", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ error: "pull request not found" }),
      { status: 404, headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(
      api.lookupPullRequestByNumber("ws-1", 468),
    ).resolves.toBeNull();
  });

  it("degrades a 409 cross-repo ambiguity to null", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ error: "pull request number is ambiguous across repositories" }),
      { status: 409, headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);

    await expect(
      api.lookupPullRequestByNumber("ws-1", 468),
    ).resolves.toBeNull();
  });
});

describe("ApiClient.transcribeVoiceAudio (RUYI-710 阶段 2 降级听写)", () => {
  afterEach(() => {
    api.setToken(null);
    getCurrentSlug.mockReturnValue(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  function stubFetch(body: unknown, ok = true, status = 200) {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify(body),
      { status, headers: { "Content-Type": "application/json" } },
    ));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "log").mockImplementation(() => undefined);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
    return fetchMock;
  }

  it("POST workspace 作用域转写端点，multipart body 且不带预设 Content-Type", async () => {
    const fetchMock = stubFetch({ text: "你好世界" });

    await expect(
      api.transcribeVoiceAudio("ws-1", new Blob([new Uint8Array([1, 2])], { type: "audio/wav" })),
    ).resolves.toBe("你好世界");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("https://api.example.test/api/workspaces/ws-1/voice-transcriptions");
    expect(init.method).toBe("POST");
    expect(init.body).toBeInstanceOf(FormData);
    const headers = init.headers as Record<string, string>;
    // 无预设 Content-Type：boundary 由 RN/undici 的 FormData 序列化生成，
    // 客户端预设会与之错位（uploadFile 同一条注释先例）。
    expect(headers["Content-Type"]).toBeUndefined();
  });

  it("text 字段缺失或非字符串一律收敛空串（空转写分支语义）", async () => {
    stubFetch({});
    await expect(
      api.transcribeVoiceAudio("ws-1", new Blob([])),
    ).resolves.toBe("");
  });

  it("非 2xx 抛 ApiError 携带状态码（hook 层统一 toast）", async () => {
    stubFetch(
      { error: "voice_unavailable", message: "voice not configured" },
      false,
      409,
    );
    const err = await api
      .transcribeVoiceAudio("ws-1", new Blob([]))
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
  });

  it("鉴权头随 token 注入", async () => {
    api.setToken("tok-1");
    const fetchMock = stubFetch({ text: "x" });

    await api.transcribeVoiceAudio("ws-1", new Blob([]));

    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect((init.headers as Record<string, string>)["Authorization"]).toBe(
      "Bearer tok-1",
    );
  });
});
