// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { probeServer } from "./probe-server";

describe("mobile startup probe", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it.each([401, 403, 204])("accepts %i without passing credentials", async (status) => {
    const fetcher = vi.fn().mockResolvedValue({ status, headers: { get: () => "application/json" } });
    vi.stubGlobal("fetch", fetcher);
    expect(await probeServer("https://example.test", new AbortController().signal)).toBe(true);
    expect(fetcher).toHaveBeenCalledWith("https://example.test/api/me", {
      signal: expect.any(AbortSignal), credentials: "omit",
    });
  });

  it("rejects an HTML response and an unavailable server", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ status: 200, headers: { get: () => "text/html" } }));
    expect(await probeServer("https://example.test", new AbortController().signal)).toBe(false);
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    expect(await probeServer("https://example.test", new AbortController().signal)).toBe(false);
  });

  it("settles as unreachable within three seconds", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    const result = probeServer("https://example.test", new AbortController().signal);
    await vi.advanceTimersByTimeAsync(3_000);
    expect(await result).toBe(false);
  });
});
