import { useRef } from "react";
import { ActivityIndicator, View } from "react-native";
import { Stack, Redirect } from "expo-router";
import { useAuthStore } from "@/data/auth-store";
import { resolveAppGate } from "@/lib/auth-route";
import { useStartupServerStore } from "@/data/startup-server-store";

/**
 * Auth-required layout. Redirects to /login only after session restoration
 * settles, so a server-switch rollback cannot eject the current route.
 *
 * While startup resolves, the gate holds the current route instead of
 * redirecting: expo-router has already placed a cold-start deep link's
 * target in the stack, and navigating now would discard it (RUYI-346 QA
 * round 2 — force-stop + deep link landed on the default Inbox tab). Once
 * the phase flips, the pending route mounts intact — the same pattern
 * [workspace]/_layout uses while the membership list loads.
 *
 * Workspace membership is enforced one level deeper at [workspace]/_layout —
 * not here — because select-workspace.tsx itself is auth-required but
 * workspace-less.
 */
export default function AppLayout() {
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const isServerSwitching = useAuthStore((s) => s.isServerSwitching);
  const startupPhase = useStartupServerStore((s) => s.phase);
  const hadAuthenticatedSessionRef = useRef(Boolean(user));
  if (user) hadAuthenticatedSessionRef.current = true;

  // During a server switch, initialize() temporarily clears user while it
  // restores the target session or rolls back after a transient failure.
  // Keep an already-authenticated route mounted through that transition; an
  // initial unauthenticated launch still follows the normal login redirect.
  const gate = resolveAppGate(
    startupPhase,
    Boolean(user),
    isLoading,
    isServerSwitching,
    hadAuthenticatedSessionRef.current,
  );

  if (gate.kind === "server-select") {
    return <Redirect href="/servers/select" />;
  }

  if (gate.kind === "startup-hold") {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <ActivityIndicator />
      </View>
    );
  }

  if (gate.kind === "login") {
    return <Redirect href="/login" />;
  }

  return <Stack screenOptions={{ headerShown: false }} />;
}
