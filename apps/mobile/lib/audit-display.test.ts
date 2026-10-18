// @vitest-environment node
import { describe, expect, it } from "vitest";
import { auditDateToISOString, auditFailureKind, buildAuditParams, nextAuditCursor, safeAuditDetails, type AuditFilterDraft } from "./audit-display";

describe("audit display helpers", () => {
  it("maps the complete supported filter set and drops blank values", () => {
    expect(buildAuditParams({
      since: " 2026-10-01T00:00:00Z ", until: "", timeBasis: "invocation_start",
      actorID: "member-id", actorType: "member", clientName: "Codex", clientVersion: "1.2",
      toolName: "read_file", resultStatus: "failure", resultErrorCode: "permission_denied",
      targetType: "host", targetID: "host-id",
    })).toEqual({
      limit: 50, since: "2026-10-01T00:00:00Z", time_basis: "invocation_start", actor_id: "member-id",
      actor_type: "member", client_name: "Codex", client_version: "1.2", tool_name: "read_file",
      result_status: "failure", result_error_code: "permission_denied", target_type: "host", target_id: "host-id",
    });
  });

  it("does not send a partial target filter rejected by the service", () => {
    const params = buildAuditParams({
      since: "", until: "", timeBasis: "", actorID: "", actorType: "", clientName: "", clientVersion: "",
      toolName: "", resultStatus: "", resultErrorCode: "", targetType: "host", targetID: "",
    });
    expect(params).not.toHaveProperty("target_type");
    expect(params).not.toHaveProperty("target_id");
  });

  it("maps picked dates to UTC RFC3339 values and rejects invalid dates", () => {
    expect(auditDateToISOString(new Date("2026-10-01T12:34:56.000Z"))).toBe("2026-10-01T12:34:56.000Z");
    expect(() => auditDateToISOString(new Date(Number.NaN))).toThrow("Invalid audit date");
  });

  it("rejects invalid non-empty enumerations instead of silently omitting filters", () => {
    const base: AuditFilterDraft = {
      since: "", until: "", timeBasis: "", actorID: "", actorType: "", clientName: "", clientVersion: "",
      toolName: "", resultStatus: "", resultErrorCode: "", targetType: "", targetID: "",
    };
    for (const [field, value] of [["timeBasis", "started"], ["actorType", "robot"], ["resultStatus", "ok"], ["resultErrorCode", "oops"], ["targetType", "repo"]] as const) {
      expect(() => buildAuditParams({ ...base, [field]: value })).toThrow(`Invalid audit filter: ${field}`);
    }
  });

  it("requires both server cursors before requesting another page", () => {
    expect(nextAuditCursor({ next_cursor: "time", next_cursor_id: "id" })).toEqual({ cursor: "time", cursor_id: "id" });
    expect(nextAuditCursor({ next_cursor: "time", next_cursor_id: null })).toBeUndefined();
    expect(nextAuditCursor({ next_cursor: null, next_cursor_id: "id" })).toBeUndefined();
  });

  it("distinguishes forbidden access from service failures", () => {
    expect(auditFailureKind({ status: 403 })).toBe("permission");
    expect(auditFailureKind({ status: 500 })).toBe("service");
    expect(auditFailureKind(new Error("offline"))).toBe("service");
  });

  it("only exposes allowlisted detail fields and never target ids or arbitrary payload", () => {
    expect(safeAuditDetails({ details: {
      tool_name: "read_file", result_status: "success", target_type: "host", target_id: "/private/path",
      content: "secret",
    } } as never)).toEqual([["Tool", "read_file"], ["Result", "success"], ["Target type", "host"]]);
  });
});
