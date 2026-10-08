/**
 * Retry planning for failed agent quick-creates (RUYI-527).
 *
 * Mirrors web's retry gate in packages/views/inbox/components/inbox-page.tsx:
 * a `quick_create_failed` inbox item whose details carry `task_id` +
 * `source_context_id` is retryable — POST /api/tasks/:id/retry-source-context
 * re-enqueues the creation and the server atomically transfers the pending
 * source context (the original input) to the new task. The client never
 * resends the prompt itself; it only names the failed task.
 *
 * `quick_create_unconfirmed` deliberately does NOT plan a retry: the issue
 * may actually have been created (web renders it without failure framing
 * for the same reason — see packages/core/types/inbox.ts).
 */
import type { InboxItem } from "@multica/core/types";

export interface QuickCreateRetryPlan {
  /** The failed quick-create task — the retry endpoint's locator. */
  taskId: string;
  /** Pending source context the server will transfer to the new task. */
  sourceContextId: string;
  /** Read-only echo for the detail card; absence does not block a retry. */
  originalPrompt: string;
}

export function getQuickCreateRetryPlan(
  item: InboxItem,
): QuickCreateRetryPlan | null {
  if (item.type !== "quick_create_failed") return null;
  const details = item.details;
  if (!details) return null;
  const { task_id: taskId, source_context_id: sourceContextId } = details;
  if (!taskId || !sourceContextId) return null;
  return {
    taskId,
    sourceContextId,
    originalPrompt: details.original_prompt ?? "",
  };
}
