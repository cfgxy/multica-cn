/**
 * Android share intent → in-app share landing (RUYI-463).
 *
 * Render-less, mounted once in the root layout next to
 * NotificationResponseNavigator — same entry-point shape, simpler identity
 * rules: a share carries no server/workspace identity, so there is no
 * bridging decision. The user picks the destination inside the landing page.
 *
 * Two entry paths, deduplicated by the native payload id:
 *   - cold start: `getInitialShare()` reads the launch intent (and any
 *     payload that raced ahead of this subscription);
 *   - runtime: the `onShareIntent` event fires while the app is alive.
 *
 * Navigation is gated on the same readiness as the notification navigator:
 * before the expo-router tree exists a `router.push` is a silent no-op, and
 * before the auth session settles the landing would bounce. Early payloads
 * park in `useSharedIntentStore` (files survive in memory) and the push
 * flushes once user + navigation tree are both ready. The store write
 * happens immediately either way, so a landing page already on top of the
 * stack simply re-renders with the new files.
 *
 * Signed-out shares are not dropped: files park until the user signs in,
 * which mirrors how the OS treats the share — the user explicitly chose
 * Multica. If they never sign in, process death reaps the memory-only
 * payload.
 */
import { useEffect, useRef } from "react";
import { router, usePathname, useRootNavigationState } from "expo-router";
import { useAuthStore } from "@/data/auth-store";
import { useSharedIntentStore } from "@/data/stores/shared-intent-store";
import ShareIntent from "@/lib/share-intent";
import { normalizeSharePayload } from "@/lib/share-payload";

/** Bounded so a pathological session can't grow it forever. */
const HANDLED_CAPACITY = 20;

export function ShareIntentNavigator() {
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const rootNavigationState = useRootNavigationState();
  const pathname = usePathname();

  const navReadyRef = useRef(false);
  navReadyRef.current = Boolean(rootNavigationState?.key);
  const pathnameRef = useRef(pathname);
  pathnameRef.current = pathname;
  const userIdRef = useRef(userId);
  userIdRef.current = userId;
  const pendingRef = useRef(false);
  const handledRef = useRef<Set<string>>(new Set());

  const markHandled = (id: string | undefined) => {
    if (!id) return;
    handledRef.current.add(id);
    if (handledRef.current.size > HANDLED_CAPACITY) {
      // Drop the oldest entry (Set preserves insertion order).
      handledRef.current.delete(handledRef.current.values().next().value!);
    }
  };

  const offer = (raw: unknown) => {
    const id = (raw as { id?: string } | null)?.id;
    if (id && handledRef.current.has(id)) return;
    markHandled(id);
    const payload = normalizeSharePayload(raw);
    if (!payload) return; // text-only / empty / bridge garbage — no landing
    useSharedIntentStore.getState().setPayload(payload.files);
    if (!navReadyRef.current || !userIdRef.current) {
      pendingRef.current = true;
      return;
    }
    navigateOnce();
  };

  const navigateOnce = () => {
    if (pathnameRef.current === "/share-target") return;
    router.push("/share-target");
  };

  // One subscription for the process lifetime of this navigator; reads go
  // through refs so the listener sees current readiness after re-renders.
  useEffect(() => {
    const subscription = ShareIntent.addListener("onShareIntent", (event) => {
      offer(event);
    });
    ShareIntent.getInitialShare()
      .then((payload) => {
        if (payload) offer(payload);
      })
      .catch(() => {
        // Non-Android platforms / module missing — the feature is Android-only.
      });
    return () => subscription.remove();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Flush the parked payload once auth + navigation tree settle (cold start).
  useEffect(() => {
    if (!pendingRef.current) return;
    if (!navReadyRef.current || !userId) return;
    pendingRef.current = false;
    navigateOnce();
  }, [userId, rootNavigationState]);

  return null;
}
