/**
 * Taps on an inbox system notification → navigate to the issue (RUYI-37).
 *
 * Two entry paths, both deduplicated by notification request identifier:
 *   - cold start (app was killed, user launched it via the notification):
 *     `getLastNotificationResponseAsync` — but at mount time the auth
 *     session is still restoring (userId null), so the response parks in a
 *     pending slot and navigates once the session lands;
 *   - runtime tap (app foreground/background with JS alive): the response
 *     listener fires immediately.
 *
 * Signed-out taps are ignored: the (app) layout would bounce to login
 * anyway, and silently dropping beats a login-then-surprise-navigation.
 * The workspace slug travels in the notification data (captured at post
 * time) so the tap lands in the workspace the event belonged to, not
 * whichever one is currently selected.
 *
 * Render-less by design; mounted once in the root layout.
 */
import { useEffect, useRef } from "react";
import { router } from "expo-router";
import * as Notifications from "expo-notifications";
import { useAuthStore } from "@/data/auth-store";

/** Bounded so a pathological session can't grow it forever. */
const HANDLED_CAPACITY = 20;

interface InboxNotificationData {
  inbox_id?: unknown;
  issue_id?: unknown;
  workspace_slug?: unknown;
}

function asString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function navigateToIssue(data: InboxNotificationData): boolean {
  const issueId = asString(data.issue_id);
  const workspaceSlug = asString(data.workspace_slug);
  if (!issueId || !workspaceSlug) return false;
  router.push(`/(app)/${workspaceSlug}/issue/${issueId}`);
  return true;
}

export function NotificationResponseNavigator() {
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const handledRef = useRef<Set<string>>(new Set());
  const pendingRef = useRef<Notifications.NotificationResponse | null>(null);

  const offer = (response: Notifications.NotificationResponse): void => {
    const identifier = response.notification.request.identifier;
    if (handledRef.current.has(identifier)) return;
    handledRef.current.add(identifier);
    if (handledRef.current.size > HANDLED_CAPACITY) {
      // Set iteration order is insertion order — drop the oldest.
      handledRef.current.delete(handledRef.current.values().next().value!);
    }

    // Signed-out taps (or taps during session restore) park until the
    // session effect below runs; a settled signed-out state just drops it.
    if (useAuthStore.getState().user?.id) {
      navigateToIssue(
        response.notification.request.content.data as InboxNotificationData,
      );
    } else {
      pendingRef.current = response;
    }
  };

  // Session restore finished (or the user signed out) — flush a parked
  // cold-start tap now that the (app) tree can actually mount the route.
  useEffect(() => {
    if (!userId || !pendingRef.current) return;
    const parked = pendingRef.current;
    pendingRef.current = null;
    navigateToIssue(
      parked.notification.request.content.data as InboxNotificationData,
    );
  }, [userId]);

  useEffect(() => {
    let cancelled = false;
    void Notifications.getLastNotificationResponseAsync().then((response) => {
      if (!cancelled && response) offer(response);
    });
    const subscription =
      Notifications.addNotificationResponseReceivedListener(offer);
    return () => {
      cancelled = true;
      subscription.remove();
    };
    // offer reads auth via getState(); subscribing once per mount is the
    // intended shape — resubscribing on every userId flip would re-run the
    // getLastNotificationResponseAsync probe too.
  }, []);

  return null;
}
