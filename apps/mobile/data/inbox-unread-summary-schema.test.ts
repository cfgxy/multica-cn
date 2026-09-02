import { describe, expect, it } from "vitest";
import {
  EMPTY_INBOX_UNREAD_SUMMARY,
  InboxUnreadSummarySchema,
} from "./schemas";

/**
 * Tests for mobile's CLIENT-SIDE parsing of GET /api/inbox/unread-summary —
 * the cross-workspace unread counts backing the switch-workspace sheet's
 * per-workspace blue dot (RUYI-44; web's sidebar dot derives from the same
 * endpoint via packages/core).
 *
 * Mirrors the web schema (packages/core/api/schemas.ts InboxUnreadSummarySchema):
 * lenient by design — `.loose()` so a future server field addition can't
 * blank the dot, and on malformed JSON `parseWithFallback` returns the empty
 * list, which simply hides every dot rather than crashing the sheet.
 */
describe("inbox unread summary schema", () => {
  it("parses a payload shaped like the documented server response", () => {
    const parsed = InboxUnreadSummarySchema.parse([
      { workspace_id: "ws-1", count: 0 },
      { workspace_id: "ws-2", count: 3 },
    ]);
    expect(parsed).toHaveLength(2);
    expect(parsed[0]).toEqual({ workspace_id: "ws-1", count: 0 });
    expect(parsed[1]).toEqual({ workspace_id: "ws-2", count: 3 });
  });

  it("tolerates unknown extra fields from the server", () => {
    const parsed = InboxUnreadSummarySchema.parse([
      { workspace_id: "ws-2", count: 1, workspace_name: "Future Field" },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.workspace_id).toBe("ws-2");
  });

  it("rejects a row missing count so parseWithFallback can hide the dot", () => {
    expect(
      InboxUnreadSummarySchema.safeParse([{ workspace_id: "ws-2" }]).success,
    ).toBe(false);
  });

  it("parses an empty array", () => {
    expect(InboxUnreadSummarySchema.parse([])).toEqual([]);
  });

  it("empty fallback hides every dot without crashing the sheet", () => {
    expect(EMPTY_INBOX_UNREAD_SUMMARY).toEqual([]);
  });
});
