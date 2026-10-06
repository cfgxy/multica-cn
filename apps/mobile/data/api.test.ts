// @vitest-environment node

import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "./api";

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
