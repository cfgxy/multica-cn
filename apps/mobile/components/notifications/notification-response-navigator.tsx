/**
 * Taps on an inbox system notification → navigate to the issue (RUYI-37).
 *
 * Two entry paths, both deduplicated by notification request identifier:
 *   - cold start (app was killed, user launched it via the notification):
 *     `getLastNotificationResponseAsync` — polled with a bounded retry
 *     because the native module can answer null before the event bridge is
 *     fully up (D3: the one-shot probe raced startup and silently missed);
 *   - runtime tap (app foreground/background with JS alive): the response
 *     listener fires immediately.
 *
 * Navigation itself is gated on the root navigation state being mounted
 * (D3): a `router.push` issued before expo-router's tree is ready is a
 * silent no-op, which is how a cold-start tap ended on the inbox tab
 * instead of the issue. Responses that arrive (or park) too early are held
 * in a pending slot and re-flushed once BOTH the auth session and the
 * navigation tree are ready.
 *
 * Signed-out taps are dropped: the (app) layout would bounce to login
 * anyway, and silently dropping beats a login-then-surprise-navigation.
 * The workspace slug travels in the notification data (captured at post
 * time) so the tap lands in the workspace the event belonged to, not
 * whichever one is currently selected.
 *
 * Render-less by design; mounted once in the root layout.
 */
import { useEffect, useRef } from "react";
import { router, useRootNavigationState } from "expo-router";
import * as Notifications from "expo-notifications";
import { useAuthStore } from "@/data/auth-store";

/** Bounded so a pathological session can't grow it forever. */
const HANDLED_CAPACITY = 20;

/** Bounded retry for the cold-start probe (see header). */
const INITIAL_PROBE_ATTEMPTS = 4;
const INITIAL_PROBE_DELAY_MS = 400;

interface InboxNotificationData {
  inbox_id?: unknown;
  issue_id?: unknown;
  workspace_slug?: unknown;
}

function asString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function routeFor(data: InboxNotificationData): string | null {
  const issueId = asString(data.issue_id);
  const workspaceSlug = asString(data.workspace_slug);
  if (!issueId || !workspaceSlug) return null;
  return `/(app)/${workspaceSlug}/issue/${issueId}`;
}

export function NotificationResponseNavigator() {
  const userId = useAuthStore((s) => s.user?.id ?? null);
  // undefined while expo-router hasn't mounted its navigation container.
  const rootNavigationState = useRootNavigationState();
  const handledRef = useRef<Set<string>>(new Set());
  const pendingRef = useRef<Notifications.NotificationResponse | null>(null);

  const markHandled = (identifier: string): void => {
    handledRef.current.add(identifier);
    if (handledRef.current.size > HANDLED_CAPACITY) {
      // Set iteration order is insertion order — drop the oldest.
      handledRef.current.delete(handledRef.current.values().next().value!);
    }
  };

  const offer = (response: Notifications.NotificationResponse): void => {
    if (handledRef.current.has(response.notification.request.identifier)) {
      return;
    }
    const route = routeFor(
      response.notification.request.content.data as InboxNotificationData,
    );
    // Malformed / issue-less notification: nothing to navigate to.
    if (!route) {
      markHandled(response.notification.request.identifier);
      return;
    }
    // Signed-out taps (or taps during session restore) park; a settled
    // signed-out state just drops it on the next flush.
    if (!useAuthStore.getState().user?.id) {
      pendingRef.current = response;
      return;
    }
    // Navigation tree not mounted yet (cold-start race): park — the flush
    // effect below re-fires once useRootNavigationState() reports ready.
    if (!rootNavigationState?.key) {
      pendingRef.current = response;
      return;
    }
    markHandled(response.notification.request.identifier);
    try {
      router.push(route);
    } catch (err) {
      // Navigation is best-effort: the alert stays reachable via the inbox
      // tab, and a router hiccup must never crash the app.
      console.warn("[notifications] tap navigation failed", err);
    }
  };

  // Flush conditions, re-checked whenever any of them flips:
  //   pending response parked + session restored + navigation tree mounted.
  // A flush that still can't navigate (route rejected) re-parks via offer.
  const canFlush = Boolean(userId && rootNavigationState?.key);
  useEffect(() => {
    if (!canFlush || !pendingRef.current) return;
    const parked = pendingRef.current;
    pendingRef.current = null;
    offer(parked);
    // offer reads auth via getState(); canFlush is the actual gate.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canFlush]);

  useEffect(() => {
    let cancelled = false;

    // Cold-start probe with bounded retry: the native answer can be null
    // while the notifications module is still initializing (the one-shot
    // probe in the first cut raced that window and missed the response,
    // so a killed-app tap landed on the default tab instead of the issue).
    const probeInitial = async (attempt: number): Promise<void> => {
      const response = await Notifications.getLastNotificationResponseAsync();
      if (cancelled) return;
      if (response) {
        offer(response);
        return;
      }
      if (attempt + 1 < INITIAL_PROBE_ATTEMPTS) {
        setTimeout(() => {
          if (!cancelled) void probeInitial(attempt + 1);
        }, INITIAL_PROBE_DELAY_MS);
      }
    };
    void probeInitial(0);

    const subscription =
      Notifications.addNotificationResponseReceivedListener(offer);
    return () => {
      cancelled = true;
      subscription.remove();
    };
    // offer closes over rootNavigationState, listed above — resubscribing
    // when the router finishes mounting re-arms the listener with a closure
    // that sees the live navigation state (the re-probe it triggers is
    // dedup-safe via handledRef).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rootNavigationState]);

  return null;
}
