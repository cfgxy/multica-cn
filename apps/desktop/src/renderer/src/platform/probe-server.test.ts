// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { probeServer } from "./probe-server";

describe("startup probe", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it.each([401, 403, 204])("treats %i as reachable without credentials", async (status) => {
    const fetcher = vi.fn().mockResolvedValue({ status, headers: { get: () => "application/json" } });
    vi.stubGlobal("fetch", fetcher);
    expect(await probeServer("https://example.test", new AbortController().signal)).toBe(true);
    expect(fetcher).toHaveBeenCalledWith("https://example.test/api/me", {
      signal: expect.any(AbortSignal), credentials: "omit",
    });
  });

  it("rejects an HTML response and network error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ status: 200, headers: { get: () => "text/html" } }));
    expect(await probeServer("https://example.test", new AbortController().signal)).toBe(false);
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    expect(await probeServer("https://example.test", new AbortController().signal)).toBe(false);
  });

  it("times out after three seconds and aborts the pending request", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn((_url: string, options: RequestInit) => new Promise((_resolve, reject) => {
      options.signal?.addEventListener("abort", () => reject(new Error("aborted")));
    }));
    vi.stubGlobal("fetch", fetcher);
    const result = probeServer("https://example.test", new AbortController().signal);
    await vi.advanceTimersByTimeAsync(3_000);
    expect(await result).toBe(false);
    expect(fetcher.mock.calls[0][1].signal?.aborted).toBe(true);
  });

  it("aborts when the selection screen leaves", async () => {
    const controller = new AbortController();
    const fetcher = vi.fn((_url: string, options: RequestInit) => new Promise((_resolve, reject) => {
      options.signal?.addEventListener("abort", () => reject(new Error("aborted")));
    }));
    vi.stubGlobal("fetch", fetcher);
    const result = probeServer("https://example.test", controller.signal);
    controller.abort();
    expect(await result).toBe(false);
  });
});
