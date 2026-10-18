// @vitest-environment node

import { describe, expect, it, vi } from "vitest";

const { listAuditEvents } = vi.hoisted(() => ({ listAuditEvents: vi.fn() }));

vi.mock("../api", () => ({
  api: { listAuditEvents },
}));

import { auditEventsPageOptions } from "./queries";

describe("auditEventsPageOptions", () => {
  it("keeps the timestamp and id cursor together for the next page", async () => {
    listAuditEvents.mockResolvedValue({ events: [], next_cursor: null, next_cursor_id: null });
    const options = auditEventsPageOptions("workspace-1", {
      time_basis: "invocation_start",
      tool_name: "read_file",
      cursor: "stale-cursor",
      cursor_id: "stale-id",
    });

    expect(options.queryKey).toEqual([
      "audit-events",
      "workspace-1",
      { time_basis: "invocation_start", tool_name: "read_file" },
    ]);
    expect(options.initialPageParam).toBeNull();
    expect(options.getNextPageParam?.({ events: [], next_cursor: "2026-10-18T00:00:00.000000001Z", next_cursor_id: "event-2" }, [], null, [])).toEqual({
      cursor: "2026-10-18T00:00:00.000000001Z",
      cursor_id: "event-2",
    });
    expect(options.getNextPageParam?.({ events: [], next_cursor: "2026-10-18T00:00:00Z", next_cursor_id: null }, [], null, [])).toBeUndefined();

    const queryFn = options.queryFn;
    if (typeof queryFn !== "function") throw new Error("expected audit query function");
    await queryFn({ pageParam: { cursor: "2026-10-18T00:00:00.000000001Z", cursor_id: "event-2" } } as never);
    expect(listAuditEvents).toHaveBeenCalledWith("workspace-1", {
      time_basis: "invocation_start",
      tool_name: "read_file",
      cursor: "2026-10-18T00:00:00.000000001Z",
      cursor_id: "event-2",
    });
  });

  it("does not request an audit page without a workspace", () => {
    expect(auditEventsPageOptions("").enabled).toBe(false);
  });
});
