// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  CredentialConnectorListSchema,
  CredentialAuditEventListSchema,
  CredentialMetadataListSchema,
  CredentialTransportKeySchema,
} from "./api";

describe("credential API schemas", () => {
  it("rejects malformed transport keys and connector responses", () => {
    expect(CredentialTransportKeySchema.safeParse({ key_id: "k", algorithm: "RSA", public_key_spki: "" }).success).toBe(false);
    expect(CredentialConnectorListSchema.safeParse([{ id: "connector" }]).success).toBe(false);
  });

  it("accepts credential metadata without any secret-bearing field", () => {
    const result = CredentialMetadataListSchema.safeParse([{
      id: "credential-id",
      name: "Build token",
      target_service: "build-service",
      connector_id: "connector-id",
      connector_revision: 1,
      purpose: "Publish release artifacts",
      status: "active",
      resource_scope: "issue",
      subject_type: "agent",
      subject_id: "agent-id",
      expires_at: null,
      created_at: "2026-10-21T00:00:00Z",
    }]);
    expect(result.success).toBe(true);
    if (result.success) expect(JSON.stringify(result.data)).not.toContain("secret");
  });

  it("parses only credential audit metadata and preserves authorization evidence", () => {
    const result = CredentialAuditEventListSchema.safeParse([{
      id: "audit-id", created_at: "2026-10-21T00:00:00Z", credential_id: "credential-id",
      event_type: "credential.broker_resolved", result: "denied", reason_code: "subject_mismatch",
      actor_type: "agent", actor_id: "agent-id", project_id: "project-id", project_title: "Build",
      issue_id: "issue-id", issue_title: "Publish", task_id: "task-id", agent_id: "agent-id",
      purpose: "Publish artifacts",
    }]);
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data[0]).toMatchObject({ result: "denied", reason_code: "subject_mismatch", purpose: "Publish artifacts" });
      expect(JSON.stringify(result.data)).not.toContain("secret");
    }
  });
});
