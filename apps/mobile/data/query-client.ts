/**
 * Mobile-owned QueryClient. Hooks AppState into TanStack Query's focusManager
 * so foregrounding the app refetches stale data — the closest equivalent to
 * web's `refetchOnWindowFocus`.
 *
 * Web/desktop use a different QueryClient (packages/core/query-client.ts) tuned
 * for WS-driven invalidation. Mobile does NOT use WS — see root CLAUDE.md
 * Sharing Principles.
 */
import { focusManager, QueryClient } from "@tanstack/react-query";
import { AppState, type AppStateStatus } from "react-native";

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 60 * 1000, // 1 minute
      gcTime: 10 * 60 * 1000, // 10 minutes
      retry: 1,
      refetchOnWindowFocus: true, // honored via focusManager bridge below
    },
    mutations: {
      retry: false,
    },
  },
});

// Bridge React Native AppState → TanStack Query focusManager.
// Foregrounding the app counts as "focus", which triggers refetchOnWindowFocus.
focusManager.setEventListener((handleFocus) => {
  const sub = AppState.addEventListener("change", (status: AppStateStatus) => {
    handleFocus(status === "active");
  });
  return () => sub.remove();
});
