/**
 * Inbox → system notification bridge (RUYI-37), Layer 3 of the realtime
 * stack. Mounted in `RealtimeSubscriptions` next to useInboxRealtime.
 *
 * The ONLY notification producer is a live `inbox:new` WS frame:
 *   - the inbox list query never triggers notifications, so reconnect
 *     refetches / cold-start loads can't replay old items as alerts;
 *   - the notified-cursor (AsyncStorage, per server+user) suppresses the
 *     one remaining duplicate vector: the same item id delivered twice.
 *
 * Permission denial is a silent downgrade: events still advance the cursor
 * (so a later grant can't replay the denied window as a stale burst), no
 * alert is presented, nothing throws.
 *
 * Known v1 boundary (server push is a separate follow-up): frames only
 * arrive while the WS is up (foreground); messages that land during
 * background/cold-start surface as inbox unread dots instead.
 */
import { useEffect, useRef } from "react";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useTranslation } from "react-i18next";
import type { InboxItem } from "@multica/core/types";
import { useAuthStore } from "@/data/auth-store";
import { useServerStore } from "@/data/server-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  inboxNotificationBodyKey,
  isInboxTransitionToBlocked,
  shouldNotifyInboxItem,
} from "@/lib/inbox-notification";
import { statusLabel } from "@/lib/issue-status";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";
import {
  cursorHas,
  cursorRecord,
  cursorStorageKey,
  emptyNotifiedCursor,
  parseNotifiedCursor,
  serializeNotifiedCursor,
  type NotifiedCursor,
} from "@/data/notifications/notified-cursor";
import {
  ensureInboxNotificationChannel,
  presentInboxNotification,
} from "@/data/notifications/present-inbox-notification";

export function useNotificationRealtime() {
  const serverId = useServerStore((s) => s.activeServerId);
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { t, i18n: instance } = useTranslation("inbox");

  const cursorRef = useRef<NotifiedCursor>(emptyNotifiedCursor());
  const loadedRef = useRef(false);
  const grantedRef = useRef(false);

  // Per cold start / server-or-user switch: ask for notification permission
  // (first launch only — later calls short-circuit), then restore the
  // cursor BEFORE the first frame can be gated. Until the cursor is loaded
  // the handler stands down entirely: the window is sub-second and the WS
  // hasn't completed auth yet, so nothing real can slip through, and a
  // stand-down can never duplicate (the failure mode we must not have).
  useEffect(() => {
    loadedRef.current = false;
    cursorRef.current = emptyNotifiedCursor();
    grantedRef.current = false;
    if (!serverId || !userId) return;

    let cancelled = false;
    void (async () => {
      grantedRef.current = await ensureInboxNotificationChannel();
      let cursor = emptyNotifiedCursor();
      try {
        const raw = await AsyncStorage.getItem(
          cursorStorageKey(serverId, userId),
        );
        cursor = parseNotifiedCursor(raw);
      } catch (err) {
        console.warn("[notifications] cursor load failed", err);
      }
      if (cancelled) return;
      cursorRef.current = cursor;
      loadedRef.current = true;
    })();

    return () => {
      cancelled = true;
    };
  }, [serverId, userId]);

  useWSSubscriptions(
    (ws) => {
      // useWSSubscriptions only guarantees wsId; this hook additionally
      // needs the per-server user scope to be resolvable before it can
      // gate anything.
      if (!serverId || !userId) return [];

      return [
        ws.on("inbox:new", (payload) => {
          const item: InboxItem | undefined = payload?.item;
          if (!item?.id || !shouldNotifyInboxItem(item)) return;
          if (!loadedRef.current) return;

          const key = cursorStorageKey(serverId, userId);
          if (cursorHas(cursorRef.current, item.id)) return;

          // Record FIRST, present second: a crash between the two costs at
          // most one undelivered alert, never a duplicate. Also recorded on
          // permission-denial (grantedRef false) — see the header comment.
          cursorRef.current = cursorRecord(cursorRef.current, [item.id]);
          void AsyncStorage.setItem(key, serializeNotifiedCursor(cursorRef.current))
            .catch((err: unknown) =>
              console.warn("[notifications] cursor persist failed", err),
            );

          if (!grantedRef.current) return;

          const bodyKey = inboxNotificationBodyKey(item.type);
          // A transition to blocked gets the localized status label ("Blocked")
          // rather than the generic "Status changed" — that transition is the
          // owner-visible "this needs you" signal. Unknown types are already
          // filtered out by shouldNotifyInboxItem, so the fallback below is
          // defensive only.
          const body = isInboxTransitionToBlocked(item)
            ? statusLabel("blocked")
            : bodyKey
              ? t(bodyKey)
              : item.type;

          void presentInboxNotification(item, body, wsSlug ?? "");
        }),
      ];
    },
    [serverId, userId, wsSlug, instance.language],
  );
}
