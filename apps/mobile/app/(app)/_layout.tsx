import { useRef } from "react";
import { Stack, Redirect } from "expo-router";
import { useAuthStore } from "@/data/auth-store";
import { shouldRenderAuthenticatedStack } from "@/lib/auth-route";

/**
 * Auth-required layout. Redirects to /login only after session restoration
 * settles, so a server-switch rollback cannot eject the current route.
 *
 * Workspace membership is enforced one level deeper at [workspace]/_layout —
 * not here — because select-workspace.tsx itself is auth-required but
 * workspace-less.
 */
export default function AppLayout() {
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const isServerSwitching = useAuthStore((s) => s.isServerSwitching);
  const hadAuthenticatedSessionRef = useRef(Boolean(user));
  if (user) hadAuthenticatedSessionRef.current = true;

  // During a server switch, initialize() temporarily clears user while it
  // restores the target session or rolls back after a transient failure. Keep
  // an already-authenticated route mounted through that transition; an initial
  // unauthenticated launch still follows the normal login redirect.
  if (
    !shouldRenderAuthenticatedStack(
      Boolean(user),
      isLoading,
      isServerSwitching,
      hadAuthenticatedSessionRef.current,
    )
  ) {
    return <Redirect href="/login" />;
  }
  return <Stack screenOptions={{ headerShown: false }} />;
}
