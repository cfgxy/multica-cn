import { describe, expect, it } from "vitest";
import { commentIdsInPrefetchRange } from "./issue-reference-prefetch";

describe("commentIdsInPrefetchRange", () => {
  it("includes visible and adjacent comment rows, excluding non-comment rows", () => {
    const rows = [
      { entry: { id: "a", type: "comment" } },
      { entry: { id: "b", type: "decision" } },
      { entry: { id: "c", type: "comment" } },
      { entry: { id: "d", type: "comment" } },
      { entry: { id: "e", type: "comment" } },
      { entry: { id: "f", type: "comment" } },
    ];

    expect(commentIdsInPrefetchRange(rows, [3], 2)).toEqual(
      new Set(["c", "d", "e", "f"]),
    );
  });

  it("clears the range when no row is visible", () => {
    expect(commentIdsInPrefetchRange([], [], 2)).toEqual(new Set());
  });
});
