/**
 * Task-retry helpers shared by the run list (`run-row.tsx`) and the comment
 * card (`comment-card.tsx`) retry entries — RUYI-343, aligning mobile with
 * web's comment-card / execution-log-section retry buttons.
 *
 * Pure data + i18n, no RN runtime — colocated vitest lane covers the
 * admission gate and the failure-copy branching.
 */
import i18n from "i18next";
import type { TimelineEntry } from "@multica/core/types";
import { dispatchReasonCode } from "./dispatch-reason";

/**
 * Same admission gate as web's
 * `packages/views/issues/components/comment-card.tsx:retryableAgentFailureComment`:
 * only agent-authored system comments carrying a source task id get a retry
 * entry. Kept a literal mirror on purpose — the two clients must agree on
 * which failure comments are retryable.
 */
export function retryableAgentFailureComment(
  entry: TimelineEntry,
): entry is TimelineEntry & { source_task_id: string } {
  return (
    entry.actor_type === "agent" &&
    entry.comment_type === "system" &&
    typeof entry.source_task_id === "string" &&
    entry.source_task_id.length > 0
  );
}

/**
 * Mobile mirror of `packages/core/api/client.ts:errorCode`: reads the
 * `code` off a structured conflict body (the run-level retry endpoint's
 * 409 envelope is `{code, message, task}`). Narrowed on the `body` field
 * rather than a core `ApiError` instanceof — mobile throws its own
 * `apps/mobile/data/api.ts:ApiError`, a different constructor (same reason
 * as `dispatch-reason.ts`).
 */
function errorCode(err: unknown): string | undefined {
  const body = (err as { body?: unknown } | null)?.body;
  if (body && typeof body === "object") {
    const code = (body as { code?: unknown }).code;
    if (typeof code === "string" && code.length > 0) return code;
  }
  return undefined;
}

/**
 * User-facing sentence for a failed retry — superset of web's
 * execution-log-section error branches, sharing its
 * `issues:execution_log.*` copy (mobile i18n consumes the same
 * `@multica/views/locales` resources, so the wording stays identical
 * across clients). Order mirrors web: structured 409 conflicts first,
 * then the revoked-permission 403 (MUL-4525 — must not read as a
 * transient failure), then the server message, then the generic copy.
 */
export function retryFailureMessage(err: unknown): string {
  const conflict = errorCode(err);
  if (conflict === "agent_already_queued") {
    return i18n.t(
      "issues:execution_log.retry_conflict_agent_busy",
      "Not retried — this agent already has an unfinished run on this issue",
    );
  }
  if (conflict === "retry_descendant_active") {
    return i18n.t(
      "issues:execution_log.retry_conflict_descendant_active",
      "Not retried — this run already has an unfinished retry",
    );
  }
  if (dispatchReasonCode(err) === "invocation_not_allowed") {
    return i18n.t(
      "issues:execution_log.retry_blocked",
      "Not retried — you don't have permission to use this agent",
    );
  }
  if (err instanceof Error && err.message) return err.message;
  return i18n.t(
    "issues:execution_log.retry_failed",
    "Failed to retry task",
  );
}
