// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { createEnvironmentRequest } from "./environment-request";

describe("createEnvironmentRequest", () => {
  it("proxies requests only to the active localhost daemon with its profile bearer token", async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => [{ archive_id: "a".repeat(64) }],
    });
    const request = createEnvironmentRequest({
      ensureActiveProfile: async () => ({ name: "desktop-example", port: 21456 }),
      fetchHealthAtPort: async () => ({ status: "running" }),
      readProfileConfig: async () => ({ token: "test-profile-token" }),
      fetch: fetch as unknown as typeof globalThis.fetch,
    });

    await expect(request("/worktrees/archives", "POST", { action: "preview" })).resolves.toEqual([
      { archive_id: "a".repeat(64) },
    ]);
    expect(fetch).toHaveBeenCalledWith("http://127.0.0.1:21456/worktrees/archives", expect.objectContaining({
      method: "POST",
      headers: {
        Authorization: "Bearer test-profile-token",
        "X-Multica-Profile": "desktop-example",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ action: "preview" }),
    }));
  });

  it("does not make an HTTP request before profile authentication is available", async () => {
    const fetch = vi.fn();
    const request = createEnvironmentRequest({
      ensureActiveProfile: async () => null,
      fetchHealthAtPort: async () => ({ status: "running" }),
      readProfileConfig: async () => ({ token: "test-profile-token" }),
      fetch: fetch as unknown as typeof globalThis.fetch,
    });

    await expect(request("/worktrees")).rejects.toThrow("profile is not ready");
    expect(fetch).not.toHaveBeenCalled();
  });
});
