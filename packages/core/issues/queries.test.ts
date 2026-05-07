import { describe, it, expect, vi, beforeEach } from "vitest";
import { ApiError } from "../api/client";

// Mock the api singleton — we exercise the queryFn directly so we don't have
// to spin up TanStack Query's machinery.
const listTimelineV2 = vi.hoisted(() => vi.fn());
vi.mock("../api", () => ({
  api: { listTimelineV2 },
}));

import { issueTimelineInfiniteOptions } from "./queries";

beforeEach(() => {
  listTimelineV2.mockReset();
});

describe("issueTimelineInfiniteOptions queryFn", () => {
  // Around-mode anchors come from inbox notifications. The notification can
  // outlive the entry it points at (user deletes a comment between dispatch
  // and click). Without this fallback the issue page would show a blank
  // timeline — bad UX for a still-populated issue.
  it("falls back to latest mode when an around-mode 404 surfaces", async () => {
    const opts = issueTimelineInfiniteOptions("issue-1", "deleted-comment-id");

    listTimelineV2
      .mockRejectedValueOnce(new ApiError("not found", 404, "Not Found", null))
      .mockResolvedValueOnce({
        comments: [
          {
            type: "comment",
            id: "c-1",
            actor_type: "member",
            actor_id: "u",
            content: "still here",
            parent_id: null,
            created_at: "2026-01-15T00:00:00Z",
            updated_at: "2026-01-15T00:00:00Z",
            comment_type: "comment",
          },
        ],
        activities: [],
        next_cursor: null,
        prev_cursor: null,
        has_more_before: false,
        has_more_after: false,
      });

    // queryFn signature from TanStack: { pageParam, queryKey, signal, ... }.
    // Only pageParam matters for our branching.
    const out = await (opts.queryFn as any)({
      pageParam: { mode: "around", id: "deleted-comment-id" },
    });

    expect(listTimelineV2).toHaveBeenCalledTimes(2);
    expect(listTimelineV2.mock.calls[0]?.[1]).toEqual({
      mode: "around",
      id: "deleted-comment-id",
    });
    expect(listTimelineV2.mock.calls[1]?.[1]).toEqual({ mode: "latest" });
    expect(out.entries).toHaveLength(1);
    expect(out.entries[0]?.id).toBe("c-1");
  });

  it("does not swallow non-404 errors", async () => {
    const opts = issueTimelineInfiniteOptions("issue-1", "some-id");
    listTimelineV2.mockRejectedValueOnce(
      new ApiError("server boom", 500, "Internal Server Error", null),
    );

    await expect(
      (opts.queryFn as any)({
        pageParam: { mode: "around", id: "some-id" },
      }),
    ).rejects.toThrow(/server boom/);
    // Latest fallback must NOT fire — only 404 anchor-misses qualify.
    expect(listTimelineV2).toHaveBeenCalledTimes(1);
  });

  it("does not retry latest-mode 404s (no anchor to drop)", async () => {
    const opts = issueTimelineInfiniteOptions("issue-1");
    const err = new ApiError("not found", 404, "Not Found", null);
    listTimelineV2.mockRejectedValueOnce(err);

    await expect(
      (opts.queryFn as any)({ pageParam: { mode: "latest" } }),
    ).rejects.toBe(err);
    expect(listTimelineV2).toHaveBeenCalledTimes(1);
  });
});
