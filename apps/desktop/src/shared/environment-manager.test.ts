// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  parseArchiveResults,
  parseArchives,
  parseCachePreview,
  parseCacheResults,
  parseManagedEnvironments,
} from "./environment-manager";

describe("environment manager response parsing", () => {
  it("parses managed inventory without exposing local paths", () => {
    expect(parseManagedEnvironments([{
      environment_id: "a".repeat(64),
      task_id: "task-1",
      workspace_short: "acme",
      task_short: "RUYI-1",
      parent_status: "done",
      size_bytes: 2048,
      age_seconds: 3600,
      active: false,
      protection_reason: "",
      retention_reason: "expired",
      path: "/private/path",
    }])).toEqual([{
      environmentId: "a".repeat(64),
      taskId: "task-1",
      issueId: undefined,
      workspaceShort: "acme",
      taskShort: "RUYI-1",
      parentStatus: "done",
      sizeBytes: 2048,
      ageSeconds: 3600,
      active: false,
      protectionReason: "",
      retentionReason: "expired",
    }]);
  });

  it("rejects malformed inventories and malformed numeric fields", () => {
    expect(() => parseManagedEnvironments({})).toThrow("expected a list");
    expect(() => parseManagedEnvironments([{
      environment_id: "x",
      task_id: "task-1",
      size_bytes: "large",
    }])).toThrow("size_bytes");
  });

  it("parses cache previews and cleanup receipts", () => {
    const preview = {
      environment_id: "a".repeat(64),
      revision: "b".repeat(64),
      size_bytes: 4096,
      candidates: [{ path: "/private/cache", size_bytes: 4096 }],
      reason: "",
    };
    expect(parseCachePreview([preview])[0]).toMatchObject({ candidates: 1, sizeBytes: 4096 });
    expect(parseCacheResults([{ ...preview, removed_bytes: 2048, removed_count: 1 }])[0]).toMatchObject({
      removedBytes: 2048,
      removedCount: 1,
    });
  });

  it("parses archive inventory and operation receipts", () => {
    expect(parseArchives([{
      archive_id: "c".repeat(64), task_id: "task-1", task_name: "RUYI-1",
      kind: "issue", created_at: "2026-10-20T00:00:00Z", archive_bytes: 1024,
      logical_bytes: 2048, restore_reason: "",
    }])[0]).toMatchObject({ archiveId: "c".repeat(64), archiveBytes: 1024 });
    expect(parseArchiveResults([{
      environment_id: "a".repeat(64), revision: "b".repeat(64), archive_id: "c".repeat(64),
      original_bytes: 2048, archive_bytes: 1024, reclaimed: true, restored: false,
    }])[0]).toMatchObject({ revision: "b".repeat(64), reclaimed: true });
  });
});
