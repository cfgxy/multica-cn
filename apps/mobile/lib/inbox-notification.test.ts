import { describe, expect, it } from "vitest";
import type { InboxItem } from "@multica/core/types";
import {
  inboxNotificationBodyKey,
  isNotificationPermissionGranted,
  isInboxTransitionToBlocked,
  shouldNotifyInboxItem,
} from "./inbox-notification";

function makeItem(overrides: Partial<InboxItem> = {}): InboxItem {
  return {
    id: "0198f6a0-0000-7000-8000-000000000001",
    workspace_id: "ws-1",
    recipient_type: "member",
    recipient_id: "user-1",
    actor_type: "member",
    actor_id: "user-2",
    type: "new_comment",
    severity: "info",
    issue_id: "issue-1",
    title: "RUYI-37 Android system notifications",
    body: null,
    issue_status: "in_progress",
    read: false,
    archived: false,
    created_at: "2026-08-30T00:00:00Z",
    details: null,
    ...overrides,
  };
}

describe("shouldNotifyInboxItem", () => {
  it("notifies for a mention", () => {
    expect(shouldNotifyInboxItem(makeItem({ type: "mentioned" }))).toBe(true);
  });

  it("notifies for a task failure (the agent failure/block inbox path)", () => {
    // The server never creates an "agent_blocked" inbox item: an agent
    // entering the blocked workflow state ends its task as task:failed,
    // which lands as a task_failed inbox row (severity action_required).
    expect(shouldNotifyInboxItem(makeItem({ type: "task_failed" }))).toBe(true);
  });

  it("notifies for the theoretical agent_blocked type (forward compat)", () => {
    // Present in the InboxItemType union but never produced server-side
    // today; opted in so a future server addition lights up without a
    // mobile release.
    expect(shouldNotifyInboxItem(makeItem({ type: "agent_blocked" }))).toBe(
      true,
    );
  });

  it("notifies when an issue is moved to blocked", () => {
    expect(
      shouldNotifyInboxItem(
        makeItem({
          type: "status_changed",
          details: { from: "in_progress", to: "blocked" },
        }),
      ),
    ).toBe(true);
  });

  it("ignores status changes to non-blocked statuses", () => {
    expect(
      shouldNotifyInboxItem(
        makeItem({
          type: "status_changed",
          details: { from: "in_progress", to: "in_review" },
        }),
      ),
    ).toBe(false);
  });

  it("ignores status changes without transition details", () => {
    expect(
      shouldNotifyInboxItem(makeItem({ type: "status_changed", details: null })),
    ).toBe(false);
    expect(
      shouldNotifyInboxItem(
        makeItem({ type: "status_changed", details: { from: "todo" } }),
      ),
    ).toBe(false);
  });

  it("ignores routine churn types", () => {
    for (const type of [
      "new_comment",
      "issue_assigned",
      "unassigned",
      "assignee_changed",
      "priority_changed",
      "start_date_changed",
      "due_date_changed",
      "status_changed",
      "task_completed",
      "agent_completed",
      "reaction_added",
      "issue_subscribed",
      "review_requested",
      "quick_create_done",
      "quick_create_failed",
      "quick_create_unconfirmed",
    ] as const) {
      expect(shouldNotifyInboxItem(makeItem({ type })), type).toBe(false);
    }
  });

  it("defaults unknown types to silent (an active interruption needs a known semantic)", () => {
    // The render-side "never silently drop a category" parity rule does not
    // extend to push-style interruptions: an unknown future type has no
    // agreed urgency, so the safe default is no system notification.
    expect(
      shouldNotifyInboxItem(
        makeItem({ type: "brand_new_future_type" as InboxItem["type"] }),
      ),
    ).toBe(false);
  });

  it("ignores items without an issue link (nothing to navigate to)", () => {
    expect(
      shouldNotifyInboxItem(makeItem({ type: "mentioned", issue_id: null })),
    ).toBe(false);
  });
});

describe("isInboxTransitionToBlocked", () => {
  it("is true only for status_changed landing on blocked", () => {
    expect(
      isInboxTransitionToBlocked(
        makeItem({
          type: "status_changed",
          details: { from: "todo", to: "blocked" },
        }),
      ),
    ).toBe(true);
    expect(
      isInboxTransitionToBlocked(
        makeItem({
          type: "status_changed",
          details: { from: "todo", to: "done" },
        }),
      ),
    ).toBe(false);
    expect(
      isInboxTransitionToBlocked(makeItem({ type: "mentioned" })),
    ).toBe(false);
  });
});

describe("inboxNotificationBodyKey", () => {
  it("maps known types to the shared inbox locale keys", () => {
    // These keys are exercised by the existing INBOX_TYPES dynamic-key
    // reconciliation in i18n-dynamic-keys.test.ts — reusing them means the
    // notification copy ships in all four locales without touching
    // packages/views/locales.
    expect(inboxNotificationBodyKey("mentioned")).toBe("types.mentioned");
    expect(inboxNotificationBodyKey("task_failed")).toBe("types.task_failed");
    expect(inboxNotificationBodyKey("agent_blocked")).toBe(
      "types.agent_blocked",
    );
    expect(inboxNotificationBodyKey("status_changed")).toBe(
      "types.status_changed",
    );
  });

  it("returns null for unknown types (caller falls back to a generic label)", () => {
    expect(
      inboxNotificationBodyKey("brand_new_future_type" as InboxItem["type"]),
    ).toBeNull();
  });
});

describe("isNotificationPermissionGranted", () => {
  it("is true when the cross-platform status is granted", () => {
    expect(
      isNotificationPermissionGranted({ status: "granted" }),
    ).toBe(true);
  });

  it("is true for iOS authorized / provisional statuses", () => {
    expect(
      isNotificationPermissionGranted({
        status: "undetermined",
        ios: { status: "authorized" },
      }),
    ).toBe(true);
    expect(
      isNotificationPermissionGranted({
        status: "undetermined",
        ios: { status: "provisional" },
      }),
    ).toBe(true);
  });

  it("is false when denied, blocked, or undetermined", () => {
    for (const status of ["denied", "blocked", "undetermined"]) {
      expect(isNotificationPermissionGranted({ status }), status).toBe(false);
    }
  });

  it("is false when only iOS status is denied/undetermined", () => {
    expect(
      isNotificationPermissionGranted({
        status: "undetermined",
        ios: { status: "denied" },
      }),
    ).toBe(false);
    expect(
      isNotificationPermissionGranted({
        status: "undetermined",
        ios: { status: "undetermined" },
      }),
    ).toBe(false);
  });
});
