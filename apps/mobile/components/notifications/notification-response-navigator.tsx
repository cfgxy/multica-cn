/**
 * Taps on an inbox system notification → navigate to the issue (RUYI-37).
 *
 * Two entry paths, both deduplicated by notification request identifier:
 *   - cold start (app was killed, user launched it via the notification):
 *     probed READYNESS-DRIVEN — see below;
 *   - runtime tap (app foreground/background with JS alive): the response
 *     listener fires immediately.
 *
 * Cold start, D3 round 2: a RN cold start takes 7–14s on the QA device
 * before expo-router's tree mounts, while `getLastNotificationResponseAsync`
 * answers null until the native bridge is up. The first cut probed on mount
 * with a fixed 4×400ms window — it burned out long before readiness and the
 * launch response was missed permanently (two QA rounds landed on the
 * inbox). Now the probe STARTS from the readiness event instead: before
 * `useRootNavigationState()` reports a mounted tree we don't probe at all
 * (idle), and once ready the native side is necessarily up — a short
 * bounded retry there is a jitter safety net, not the wake-up mechanism.
 * The launch response is cached natively, so probing late loses nothing.
 *
 * Navigation is gated on the same readiness: a `router.push` issued before
 * the tree exists is a silent no-op. Responses arriving early (or probing
 * while signed out) park in a single pending slot and re-flush when BOTH
 * the auth session and the navigation tree are ready. Runtime taps with
 * everything ready navigate immediately.
 *
 * Signed-out taps are dropped on flush: the (app) layout would bounce to
 * login anyway, and silently dropping beats a login-then-surprise-
 * navigation. The workspace slug travels in the notification data (captured
 * at post time) so the tap lands in the workspace the event belonged to.
 *
 * Render-less by design; mounted once in the root layout.
 */
import { useEffect, useRef } from "react";
import { router, useRootNavigationState } from "expo-router";
import * as Notifications from "expo-notifications";
import { useAuthStore } from "@/data/auth-store";

/** Bounded so a pathological session can't grow it forever. */
const HANDLED_CAPACITY = 20;

/**
 * Post-readiness jitter net for the initial probe. Once the navigation tree
 * is mounted the native bridge is up, so this is expected to succeed on the
 * first attempt; the retries only cover a pathological slow bridge. NOT a
 * wake-up mechanism — readiness (below) is what starts the probe.
 */
const POST_READY_PROBE_ATTEMPTS = 6;
const POST_READY_PROBE_DELAY_MS = 500;

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
  // undefined until expo-router has mounted its navigation container — the
  // D3 readiness signal.
  const rootNavigationState = useRootNavigationState();
  const navReady = Boolean(rootNavigationState?.key);
  const navReadyRef = useRef(navReady);
  navReadyRef.current = navReady;

  const handledRef = useRef<Set<string>>(new Set());
  const pendingRef = useRef<Notifications.NotificationResponse | null>(null);
  /** Launch response consumed (navigated, parked, or confirmed absent). */
  const initialResolvedRef = useRef(false);

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
    // signed-out state drops the parked tap on the next flush.
    if (!useAuthStore.getState().user?.id) {
      pendingRef.current = response;
      return;
    }
    // Navigation tree not mounted yet (tap raced the cold start): park —
    // the flush effect re-fires when navReady flips true.
    if (!navReadyRef.current) {
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

  // Flush condition: a parked response + session restored + tree mounted.
  // Both readiness inputs are deps, so a tap that parked during startup is
  // re-offered as soon as the last of them lands — no polling involved.
  const canFlush = Boolean(userId && navReady);
  useEffect(() => {
    if (!canFlush || !pendingRef.current) return;
    const parked = pendingRef.current;
    pendingRef.current = null;
    offer(parked);
    // offer reads auth via getState() and nav via navReadyRef; canFlush is
    // the actual gate.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canFlush]);

  // Readiness-driven initial probe (cold-start tap path). Idle until the
  // tree is mounted — probing earlier is guaranteed-null by D3 evidence.
  // The initial response is cached natively, so this late probe recovers
  // the full launch intent. offer() parks if the session isn't restored
  // yet; the flush effect above completes the navigation once it is.
  useEffect(() => {
    if (!navReady || initialResolvedRef.current) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const probe = async (attempt: number): Promise<void> => {
      const response = await Notifications.getLastNotificationResponseAsync();
      if (cancelled || initialResolvedRef.current) return;
      if (response) {
        initialResolvedRef.current = true;
        offer(response);
        return;
      }
      if (attempt + 1 < POST_READY_PROBE_ATTEMPTS) {
        timer = setTimeout(() => {
          if (!cancelled) void probe(attempt + 1);
        }, POST_READY_PROBE_DELAY_MS);
      } else {
        // Confirmed no launch response for this session (plain app open).
        initialResolvedRef.current = true;
      }
    };
    void probe(0);

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
    // offer reads nav via navReadyRef; navReady is the actual trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navReady]);

  // Runtime taps (app alive): registered once — offer reads the CURRENT
  // readiness via navReadyRef, so no resubscription is needed.
  useEffect(() => {
    const subscription =
      Notifications.addNotificationResponseReceivedListener(offer);
    return () => subscription.remove();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return null;
}
