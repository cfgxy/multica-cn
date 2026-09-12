// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { InboxItem } from "@multica/core/types";

const notifications = vi.hoisted(() => ({
  scheduleNotificationAsync: vi.fn(),
  setNotificationHandler: vi.fn(),
}));

vi.mock("expo-notifications", () => ({
  AndroidImportance: { HIGH: "high" },
  AndroidNotificationVisibility: { PRIVATE: "private" },
  scheduleNotificationAsync: notifications.scheduleNotificationAsync,
  setNotificationHandler: notifications.setNotificationHandler,
}));

import {
  INBOX_NOTIFICATION_CHANNEL_ID,
  presentInboxNotification,
} from "./present-inbox-notification";

function makeItem(overrides: Partial<InboxItem> = {}): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "workspace-1",
    recipient_type: "member",
    recipient_id: "member-1",
    actor_type: "member",
    actor_id: "member-2",
    type: "mentioned",
    severity: "info",
    issue_id: "issue-1",
    title: "Cross-server notification",
    body: null,
    issue_status: "in_progress",
    read: false,
    archived: false,
    created_at: "2026-09-12T00:00:00Z",
    details: null,
    ...overrides,
  };
}

describe("presentInboxNotification", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    notifications.scheduleNotificationAsync.mockResolvedValue("request-1");
  });

  it("publishes the complete routing identity with optional display snapshots", async () => {
    await presentInboxNotification(makeItem(), "You were mentioned", {
      serverId: "server-b",
      workspaceSlug: "other",
      workspaceName: "Other workspace",
      serverName: "Server B",
    });

    expect(notifications.scheduleNotificationAsync).toHaveBeenCalledWith({
      content: {
        title: "Cross-server notification",
        body: "You were mentioned",
        sound: "default",
        data: {
          inbox_id: "inbox-1",
          issue_id: "issue-1",
          workspace_slug: "other",
          server_id: "server-b",
          workspace_name: "Other workspace",
          server_name: "Server B",
        },
      },
      trigger: { channelId: INBOX_NOTIFICATION_CHANNEL_ID },
    });
  });

  it("omits absent display snapshots without losing routing identity", async () => {
    await presentInboxNotification(makeItem(), "You were mentioned", {
      serverId: "server-b",
      workspaceSlug: "other",
      workspaceName: null,
      serverName: null,
    });

    const request = notifications.scheduleNotificationAsync.mock.calls[0]?.[0];
    expect(request.content.data).toEqual({
      inbox_id: "inbox-1",
      issue_id: "issue-1",
      workspace_slug: "other",
      server_id: "server-b",
    });
  });

  it("does not schedule a notification that cannot route to an issue", async () => {
    await presentInboxNotification(
      makeItem({ issue_id: null }),
      "You were mentioned",
      {
        serverId: "server-b",
        workspaceSlug: "other",
        workspaceName: null,
        serverName: null,
      },
    );

    expect(notifications.scheduleNotificationAsync).not.toHaveBeenCalled();
  });

  it("does not schedule a notification without a workspace route", async () => {
    await presentInboxNotification(makeItem(), "You were mentioned", {
      serverId: "server-b",
      workspaceSlug: "",
      workspaceName: null,
      serverName: null,
    });

    expect(notifications.scheduleNotificationAsync).not.toHaveBeenCalled();
  });
});
