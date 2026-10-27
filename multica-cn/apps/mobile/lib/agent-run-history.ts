/**
 * Per-agent run-history selectors for the agent detail 活跃 tab's 运行历史
 * section (RUYI-538 ②) — mobile mirror of web's ActivityTab "Recent work"
 * list shape (`packages/views/agents/components/tabs/activity-tab.tsx`
 * `recentTasksAll`):
 *   - chat tasks are hidden (web `isWorkflowTask` — they have their own
 *     surface; mixing them muddies "what is this agent doing for the team"),
 *   - history is terminal tasks with a completed_at (completed / failed /
 *     cancelled — cancellations answer "what just happened" alongside
 *     failures, same rule as web),
 *   - newest finished first.
 * Pagination is client-side over this slice — web RECENT_INITIAL/RECENT_PAGE
 * semantics: a small first page, then fixed-size "load more" flips.
 */
import type { AgentTask } from "@multica/core/types";

/** First page of the 运行历史 section; "load more" reveals this many more. */
export const RUN_HISTORY_INITIAL = 10;
export const RUN_HISTORY_PAGE = 20;

/** Terminal tasks that belong in run history — web's Recent work filter. */
export function isRunHistoryTask(task: AgentTask): boolean {
  return (
    !task.chat_session_id &&
    !!task.completed_at &&
    (task.status === "completed" ||
      task.status === "failed" ||
      task.status === "cancelled")
  );
}

/**
 * History slice for one agent: chat-filtered terminal tasks, newest finished
 * first. Input order is irrelevant (server ordering is not a contract here —
 * the sort is authoritative, like web's).
 */
export function selectAgentRunHistory(
  tasks: readonly AgentTask[],
): AgentTask[] {
  return tasks
    .filter(isRunHistoryTask)
    .sort(
      (a, b) =>
        new Date(b.completed_at!).getTime() -
        new Date(a.completed_at!).getTime(),
    );
}

/**
 * Retry admission for the run-history row: the retry endpoint is run-level
 * and issue-scoped (POST /api/issues/{id}/tasks/{taskId}/retry, RUYI-292),
 * so only issue-linked terminal-but-not-successful runs qualify. Cancelled
 * runs retry too — as "Run again" behind a confirm (same gate as mobile
 * RunRow / web execution-log-section: `failed || cancelled`). Chat runs are
 * not in the list at all; an issue-less autopilot/quick-create run stays
 * display-only because no retry surface exists for it server-side.
 */
export function isAgentRunRetryable(task: AgentTask): boolean {
  return (
    task.issue_id !== "" &&
    (task.status === "failed" || task.status === "cancelled")
  );
}
