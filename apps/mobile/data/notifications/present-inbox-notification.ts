/**
 * expo-notifications bindings for inbox system notifications (RUYI-37).
 *
 * Native-touching wrapper — deliberately thin and vitest-free (the
 * vitest lane can't load expo native modules; all DECISION logic lives
 * in `lib/inbox-notification.ts` and `data/notifications/notified-cursor.ts`).
 *
 * Design notes:
 *   - Local notifications only. No FCM / Expo Push (explicitly out of scope
 *     this issue; server-side push is a follow-up). That means the WS is the
 *     only trigger, which is what makes single-notification dedup tractable.
 *   - `setNotificationHandler` registers at import time (expo's own
 *     guidance) so an early notification can't race the handler. On iOS it
 *     gates foreground presentation; on Android a scheduled notification
 *     goes through the system tray either way, and `shouldPlaySound: false`
 *     there would suppress the banner entirely — keep it true.
 *   - One high-importance channel: Android 13+ gates delivery on the
 *     POST_NOTIFICATIONS runtime permission AND the channel importance.
 *   - Permission denial is a silent downgrade by design (acceptance
 *     criterion): the caller still records the cursor, so a later grant can
 *     never replay history as a burst of stale alerts.
 */
import * as Notifications from "expo-notifications";
import type { InboxItem } from "@multica/core/types";

export const INBOX_NOTIFICATION_CHANNEL_ID = "inbox";

Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowBanner: true,
    shouldShowList: true,
    shouldPlaySound: true,
    shouldSetBadge: false,
  }),
});

/** Ask once for POST_NOTIFICATIONS (Android 13+) / iOS alert permission and
 *  make sure the inbox channel exists. Safe to call on every cold start —
 *  a settled permission answer short-circuits, only the first call prompts. */
export async function ensureInboxNotificationChannel(): Promise<boolean> {
  try {
    const current = await Notifications.getPermissionsAsync();
    const iosGranted =
      current.ios?.status === Notifications.IosAuthorizationStatus.AUTHORIZED ||
      current.ios?.status ===
        Notifications.IosAuthorizationStatus.PROVISIONAL;
    const granted =
      current.status === "granted" || iosGranted;
    if (!granted && current.canAskAgain) {
      const asked = await Notifications.requestPermissionsAsync();
      if (asked.status !== "granted") return false;
    }

    await Notifications.setNotificationChannelAsync(
      INBOX_NOTIFICATION_CHANNEL_ID,
      {
        name: "Inbox",
        importance: Notifications.AndroidImportance.HIGH,
        sound: "default",
        vibrationPattern: [0, 250],
        lockscreenVisibility:
          Notifications.AndroidNotificationVisibility.PRIVATE,
      },
    );
    return true;
  } catch (err) {
    // Permission plumbing must never take the app down (e.g. expo-notifications
    // native module missing from a stale dev client build).
    console.warn("[notifications] channel/permission setup failed", err);
    return false;
  }
}

/** Post a status-bar notification for one inbox item. Fire-and-forget:
 *  failures are logged, never thrown into the WS dispatch loop. */
export async function presentInboxNotification(
  item: InboxItem,
  bodyText: string,
  workspaceSlug: string,
): Promise<void> {
  if (!item.issue_id) return;
  try {
    await Notifications.scheduleNotificationAsync({
      content: {
        // Title = the issue's own title (same minimal-exposure surface the
        // inbox UI already shows). No comment bodies, no actor names — the
        // lockscreen shows this.
        title: item.title,
        body: bodyText,
        sound: "default",
        data: {
          inbox_id: item.id,
          issue_id: item.issue_id,
          workspace_slug: workspaceSlug,
        },
      },
      // Deliver now, through the inbox channel (ChannelAwareTriggerInput —
      // Android-only semantics; iOS ignores the channel field).
      trigger: { channelId: INBOX_NOTIFICATION_CHANNEL_ID },
    });
  } catch (err) {
    console.warn("[notifications] present failed", err);
  }
}
