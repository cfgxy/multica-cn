// @vitest-environment node

import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/data/server-store", () => ({
  getApiUrl: () => "https://api.example.test",
}));

vi.mock("@/data/workspace-store", () => ({
  getCurrentSlug: () => null,
}));

vi.mock("@/lib/request-id", () => ({
  createRequestId: () => "request-1",
}));

import { api } from "./api";

const timelineEntry = {
  type: "activity",
  id: "activity-1",
  actor_type: "member",
  actor_id: "member-1",
  created_at: "2026-09-05T09:00:00Z",
};

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
