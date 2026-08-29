// @vitest-environment node
import { describe, expect, it } from "vitest";
import { AgentTaskSchema } from "./schemas";

/**
 * RUYI-33 — run-detail entry gating and failure classification read the
 * parsed AgentTask, so the wire schema must not lose fields the detail
 * screen and the runs-list badge both display.
 *
 * Two losses, both hit on the mobile runs sheet:
 *  1. `status` enum lacked `waiting_local_directory` — a parked task was
 *     `.catch()`-ed to "queued" and would have been denied a detail entry
 *     (it is not a queued task) and mislabeled in the list.
 *  2. `failure_reason` was a closed six-value enum with `.catch("")` —
 *     every refined `agent_error.*` reason the backend has written since
 *     MUL-1949 was silently dropped to undefined, so neither the list
 *     badge nor the detail classification could ever show one.
 */
function baseTask(overrides: Record<string, unknown>) {
  return {
    id: "0b9e6c1a-1111-4222-8333-444455556666",
    status: "completed",
    created_at: "2026-08-30T00:00:00Z",
    ...overrides,
  };
}

describe("AgentTaskSchema — open status enum", () => {
  it("preserves waiting_local_directory instead of catching to queued", () => {
    const parsed = AgentTaskSchema.parse(
      baseTask({ status: "waiting_local_directory" }),
    );
    expect(parsed.status).toBe("waiting_local_directory");
  });

  it("still parses every known status", () => {
    for (const status of [
      "queued",
      "dispatched",
      "running",
      "completed",
      "failed",
      "cancelled",
    ]) {
      const parsed = AgentTaskSchema.parse(baseTask({ status }));
      expect(parsed.status).toBe(status);
    }
  });
});

describe("AgentTaskSchema — open failure_reason string", () => {
  it("preserves refined agent_error.* reasons", () => {
    const parsed = AgentTaskSchema.parse(
      baseTask({
        status: "failed",
        failure_reason: "agent_error.provider_auth_or_access",
      }),
    );
    expect(parsed.failure_reason).toBe("agent_error.provider_auth_or_access");
  });

  it("preserves reasons the installed build has never seen", () => {
    const parsed = AgentTaskSchema.parse(
      baseTask({
        status: "failed",
        failure_reason: "agent_error.something_new_next_quarter",
      }),
    );
    expect(parsed.failure_reason).toBe("agent_error.something_new_next_quarter");
  });

  it("normalizes the empty-string not-failed sentinel to undefined", () => {
    const parsed = AgentTaskSchema.parse(
      baseTask({ status: "completed", failure_reason: "" }),
    );
    expect(parsed.failure_reason).toBeUndefined();
  });

  it("keeps coarse legacy reasons", () => {
    const parsed = AgentTaskSchema.parse(
      baseTask({ status: "cancelled", failure_reason: "manual" }),
    );
    expect(parsed.failure_reason).toBe("manual");
  });
});
