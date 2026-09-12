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
 * RUYI-131 — identity bridge. The slug alone isn't enough: the notification
 * was posted under one server + workspace, and the app may sit on another
 * by tap time. Navigating blind loads the issue under the wrong identity
 * and lands on the issue screen's error state (the reported "404"). So the
 * tap now routes through `lib/notification-target.ts`, which compares the
 * payload identity against the live one and returns one of: navigate
 * directly, confirm a workspace switch, confirm a server switch, or report
 * the source server as gone. Confirmation uses RN's `Alert` (system dialog
 * on both platforms, per `apps/mobile/CLAUDE.md`'s native-first waterfall)
 * — there is no visible bridge screen: declining simply doesn't navigate,
 * which leaves the user exactly where they were reading, and on a cold
 * start "where they were" is the entry redirect's own landing (inbox /
 * select-workspace / login).
 *
 * Render-less by design; mounted once in the root layout.
 */
import { useEffect, useRef } from "react";
import { Alert } from "react-native";
import { router, useRootNavigationState } from "expo-router";
import * as Notifications from "expo-notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@/data/auth-store";
import { useServerStore } from "@/data/server-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { switchServer } from "@/data/switch-server";
import { useT } from "@/lib/use-t";
import {
  parseNotificationTarget,
  resolveNotificationTap,
  type InboxNotificationData,
  type NotificationAction,
} from "@/lib/notification-target";

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

export function NotificationResponseNavigator() {
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const qc = useQueryClient();
  const { t } = useT("inbox");
  // Read through a ref inside offer(): the listener is registered once and
  // must see the CURRENT translator after a language change.
  const tRef = useRef(t);
  tRef.current = t;
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

  /** Best-effort navigation — a router hiccup must never crash the app. */
  const navigate = (kind: "push" | "replace", route: string): void => {
    try {
      if (kind === "replace") router.replace(route);
      else router.push(route);
    } catch (err) {
      // The alert stays reachable via the inbox tab.
      console.warn("[notifications] tap navigation failed", err);
    }
  };

  /**
   * Cross-server landing (RUYI-131): restore the target server's session
   * first, then replace. `replace`, not `push` — `switchServer` clears the
   * query cache, so every stack entry behind us belongs to a server whose
   * data is gone and whose identity is no longer active.
   */
  const openOnOtherServer = async (
    action: Extract<NotificationAction, { kind: "confirm-server" }>,
  ): Promise<void> => {
    // Shadowing the outer binding on purpose: the listener is registered
    // once, so it must read the CURRENT translator through the ref rather
    // than capture the one from first render.
    const t = tRef.current;
    const outcome = await switchServer(action.serverId, qc);
    if (outcome.kind === "failed") {
      // Same failure surface the server settings screen uses; the user
      // stays put and can retry by tapping the notification again.
      Alert.alert(
        t("mobile.bridge.switch_failed_title", "Switch failed"),
        outcome.error instanceof Error
          ? outcome.error.message
          : t(
              "mobile.bridge.switch_failed_message",
              "Could not switch servers.",
            ),
      );
      return;
    }
    // No restorable session on the target server → login; the notification
    // intent is not preserved across the login flow (same as a manual
    // server switch).
    navigate("replace", outcome.kind === "signed-out" ? "/login" : action.route);
  };

  const run = (action: NotificationAction): void => {
    // Shadowing the outer binding on purpose: the listener is registered
    // once, so it must read the CURRENT translator through the ref rather
    // than capture the one from first render.
    const t = tRef.current;
    switch (action.kind) {
      case "open":
        navigate("push", action.route);
        return;
      case "confirm-workspace":
        Alert.alert(
          t("mobile.bridge.cross_workspace_title", "Switch workspace?"),
          t("mobile.bridge.cross_workspace_message", {
            name: action.workspaceLabel,
          }),
          [
            { text: t("mobile.bridge.cancel", "Cancel"), style: "cancel" },
            {
              text: t("mobile.bridge.confirm_switch", "Switch"),
              // push, not replace: the workspace layout re-syncs the active
              // workspace on mount, and a back gesture returns to where the
              // user was — including the workspace they came from.
              onPress: () => navigate("push", action.route),
            },
          ],
        );
        return;
      case "confirm-server":
        Alert.alert(
          t("mobile.bridge.cross_server_title", "Switch server?"),
          t("mobile.bridge.cross_server_message", {
            server: action.serverLabel,
            workspace: action.workspaceLabel,
          }),
          [
            { text: t("mobile.bridge.cancel", "Cancel"), style: "cancel" },
            {
              text: t("mobile.bridge.confirm_switch", "Switch"),
              onPress: () => void openOnOtherServer(action),
            },
          ],
        );
        return;
      case "unavailable":
        Alert.alert(
          t("mobile.bridge.unavailable_title", "Can't open notification"),
          t(
            "mobile.bridge.unavailable_message",
            "The server this notification came from is no longer in the server list.",
          ),
        );
        return;
      case "drop":
        return;
    }
  };

  const offer = (response: Notifications.NotificationResponse): void => {
    if (handledRef.current.has(response.notification.request.identifier)) {
      return;
    }
    const data = response.notification.request.content
      .data as InboxNotificationData;
    // Malformed / issue-less notification: nothing to navigate to. Checked
    // before the park branches so a junk payload can't occupy the single
    // pending slot ahead of a real tap.
    if (!parseNotificationTarget(data)) {
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
    // RUYI-131: compare the posting identity against the live one. Resolved
    // here rather than at park time — the identity can still change while a
    // response is parked (session restore picks the last-used server).
    const action = resolveNotificationTap(data, {
      activeServerId: useServerStore.getState().activeServerId,
      currentWorkspaceSlug:
        useWorkspaceStore.getState().currentWorkspaceSlug,
      servers: useServerStore.getState().servers,
    });
    markHandled(response.notification.request.identifier);
    run(action);
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
