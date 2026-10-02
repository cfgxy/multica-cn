// @vitest-environment node
import { beforeAll, describe, expect, it } from "vitest";
import i18n from "i18next";
import { RESOURCES } from "@multica/views/locales";
import type { TimelineEntry } from "@multica/core/types";
import { retryFailureMessage, retryableAgentFailureComment } from "./task-retry";

// `retryFailureMessage` walks `i18n.t`; an uninitialised i18next returns
// undefined — not even the fallback parameter. Production runs `initI18n()`
// at app startup; mirror it here with the real RESOURCES so the assertions
// cover the sentence a user actually reads (same stance as
// lib/dispatch-reason.test.ts).
beforeAll(async () => {
  await i18n.init({
    resources: RESOURCES as never,
    lng: "en",
    fallbackLng: "en",
    defaultNS: "common",
    interpolation: { escapeValue: false },
  });
});

// Shaped like `apps/mobile/data/api.ts:ApiError` — a thrown Error carrying
// the parsed response body. Built inline rather than imported because
// `@/data/api` pulls react-native into a node-environment suite; the only
// field under test is `body`.
const apiError = (body: unknown) =>
  Object.assign(new Error("request failed"), { body });

function timelineEntry(overrides: Partial<TimelineEntry> = {}): TimelineEntry {
  return {
    type: "comment",
    id: "c1",
    actor_type: "agent",
    actor_id: "agent-1",
    created_at: "2026-10-02T00:00:00Z",
    comment_type: "system",
    source_task_id: "task-1",
    ...overrides,
  };
}

describe("retryableAgentFailureComment", () => {
  // Same admission gate as web's comment-card.tsx: only agent-authored
  // system comments that carry a source task id get a retry entry.
  it("accepts an agent system comment carrying a source task id", () => {
    const entry = timelineEntry();
    expect(retryableAgentFailureComment(entry)).toBe(true);
    if (retryableAgentFailureComment(entry)) {
      expect(entry.source_task_id).toBe("task-1");
    }
  });

  it("rejects member-authored comments", () => {
    expect(
      retryableAgentFailureComment(timelineEntry({ actor_type: "member" })),
    ).toBe(false);
  });

  it("rejects ordinary agent comments (missing or non-system type)", () => {
    expect(
      retryableAgentFailureComment(timelineEntry({ comment_type: undefined })),
    ).toBe(false);
    expect(
      retryableAgentFailureComment(timelineEntry({ comment_type: "reply" })),
    ).toBe(false);
  });

  it("rejects comments without a usable source task id", () => {
    expect(
      retryableAgentFailureComment(
        timelineEntry({ source_task_id: undefined }),
      ),
    ).toBe(false);
    expect(
      retryableAgentFailureComment(timelineEntry({ source_task_id: null })),
    ).toBe(false);
    expect(
      retryableAgentFailureComment(timelineEntry({ source_task_id: "" })),
    ).toBe(false);
  });
});

describe("retryFailureMessage", () => {
  // The point of the branch: a revoked invoke permission (MUL-4525) must
  // not read as a transient failure the user should retry.
  it("maps invocation_not_allowed to the permission-blocked copy", () => {
    const message = retryFailureMessage(
      apiError({ reason_code: "invocation_not_allowed" }),
    );
    expect(message).toMatch(/don't have permission/i);
    expect(message).not.toMatch(/try again/i);
  });

  it("maps 409 agent_already_queued to the agent-busy conflict copy", () => {
    expect(
      retryFailureMessage(
        apiError({ code: "agent_already_queued", message: "conflict" }),
      ),
    ).toMatch(/already has an unfinished run/i);
  });

  it("maps 409 retry_descendant_active to the descendant conflict copy", () => {
    expect(
      retryFailureMessage(
        apiError({ code: "retry_descendant_active", message: "conflict" }),
      ),
    ).toMatch(/unfinished retry/i);
  });

  it("prefers the 409 conflict code when reason_code is also present", () => {
    const message = retryFailureMessage(
      apiError({
        code: "agent_already_queued",
        reason_code: "invocation_not_allowed",
      }),
    );
    expect(message).toMatch(/already has an unfinished run/i);
  });

  it("falls back to the error's own message for unstructured failures", () => {
    expect(retryFailureMessage(new Error("network down"))).toBe("network down");
  });

  it("uses the generic retry-failed copy when there is no message", () => {
    expect(retryFailureMessage(undefined)).toMatch(/failed to retry/i);
  });
});
