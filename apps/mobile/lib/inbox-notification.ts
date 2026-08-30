/**
 * System-notification gate for inbox events (RUYI-37).
 *
 * Decides which `inbox:new` arrivals deserve an Android status-bar
 * notification, and which shared locale key renders the notification's
 * body line. Pure logic — the expo-notifications side lives in
 * `data/notifications/`, so this file stays vitest-loadable.
 *
 * Owner ask: "Issue 有阻塞时，或 Issue 中 @ 我时" → status-bar alert.
 * Mapped onto the inbox types the server actually produces:
 *   - `mentioned`                       — someone @-mentioned me
 *   - `task_failed`                     — an agent run failed OR entered
 *     the blocked workflow state (server-side `taskfailure.ReasonAgentBlocked`
 *     surfaces as task:failed → task_failed inbox rows; the `agent_blocked`
 *     inbox TYPE exists in the union but has no server-side producer today —
 *     we opt in anyway so a future producer lights up without a client release)
 *   - `status_changed` → details.to==="blocked" — a human parked the issue
 *
 * Everything else (comments, assignments, status churn, reactions) stays
 * silent: inbox unread dots already cover it, and notifications fatigue
 * fast. Unknown future types default to silent — the render-side "never
 * silently drop a category" parity rule is about visibility, not about
 * interrupting the user with an urgency nobody agreed on.
 */
import type { InboxItem, InboxItemType } from "@multica/core/types";

/** Inbox types that always warrant a system notification. */
const NOTIFYING_TYPES: ReadonlySet<string> = new Set([
  "mentioned",
  "task_failed",
  "agent_blocked",
]);

/** The `details.to` value of a `status_changed` item that warrants a notification. */
const BLOCKED_STATUS = "blocked";

/** Extract the from→to transition details of a `status_changed` item. */
export function inboxStatusTransition(
  item: InboxItem,
): { from?: string; to?: string } | null {
  if (item.type !== "status_changed") return null;
  const details = item.details;
  if (!details || typeof details !== "object") return null;
  const from = typeof details.from === "string" ? details.from : undefined;
  const to = typeof details.to === "string" ? details.to : undefined;
  if (!from && !to) return null;
  return { from, to };
}

/** True when the item is a `status_changed` landing on `blocked`. */
export function isInboxTransitionToBlocked(item: InboxItem): boolean {
  return inboxStatusTransition(item)?.to === BLOCKED_STATUS;
}

export function shouldNotifyInboxItem(item: InboxItem): boolean {
  // No issue link → nothing to open from the notification; the inbox tab
  // is the better surface, so don't split the experience.
  if (!item.issue_id) return false;
  if (NOTIFYING_TYPES.has(item.type)) return true;
  return isInboxTransitionToBlocked(item);
}

/**
 * Shared `inbox` namespace locale key for the notification body line.
 * Returns null for types outside the fixed set — the caller then falls
 * back to a generic label instead of guessing a dotted path (a wrong
 * dynamic key degrades to the English fallback silently; see
 * i18n-dynamic-keys.test.ts for why that class of bug needs a table).
 */
export function inboxNotificationBodyKey(
  type: InboxItemType,
): string | null {
  if (type === "status_changed") return "types.status_changed";
  if (NOTIFYING_TYPES.has(type)) return `types.${type}`;
  return null;
}

/**
 * Whether a notification-permission probe means "alerts may be posted".
 *
 * Pure decision over the shape expo-notifications' PermissionResponse gives
 * us (structural subset — no expo import, vitest-loadable). iOS can report
 * granted ONLY through its own nested status even when the cross-platform
 * `status` is still "undetermined" (provisional grants), so both layers are
 * checked. Used by the realtime hook on every foreground transition (D2):
 * a user granting the permission from system settings mid-session must
 * re-arm notifications without an app restart.
 */
export interface NotificationPermissionProbe {
  status: string;
  /** expo's IosAuthorizationStatus is a TS enum — typed `unknown` here so
   *  the structural probe accepts it without importing expo types into the
   *  vitest lane; the check normalizes via String(). */
  ios?: { status?: unknown };
}

const IOS_GRANTED = new Set(["authorized", "provisional"]);

export function isNotificationPermissionGranted(
  probe: NotificationPermissionProbe,
): boolean {
  if (probe.status === "granted") return true;
  const ios = probe.ios?.status;
  return typeof ios === "string" && IOS_GRANTED.has(ios);
}
