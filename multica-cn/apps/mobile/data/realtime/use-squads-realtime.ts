/**
 * Squad management realtime (RUYI-346). Management-side subscription —
 * mounted alongside usePresenceRealtime in the workspace layout.
 *
 * squad:created / updated / deleted carry unknown payloads
 * (packages/core/types/events.ts), so there is nothing safe to patch —
 * every event invalidates the whole squad prefix (`squadKeys.all`), which
 * covers the list, any open detail, and both per-squad sub-resource caches.
 * Squad churn is rare (roster edits, not task ticks), so the broad prefix is
 * the right trade — same reasoning as web's use-realtime-sync squad branch.
 *
 * Deliberately NOT subscribed:
 *   - skill:created / updated / deleted — the only consumers are the skills
 *     picker and the agent detail payload, both refetch on mount (staleTime
 *     0). No persistent skills surface exists on mobile to keep fresh, and
 *     the cellular-data rule says don't subscribe without one.
 *   - task:* / agent:* / daemon:* — owned by use-presence-realtime.ts.
 */
import { useQueryClient } from "@tanstack/react-query";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";
import { squadKeys } from "@/data/queries/squads";

export function useSquadsRealtime() {
  const queryClient = useQueryClient();

  useWSSubscriptions(
    (ws, wsId) => {
      const invalidateAll = () =>
        queryClient.invalidateQueries({ queryKey: squadKeys.all(wsId) });

      return [
        ws.on("squad:created", invalidateAll),
        ws.on("squad:updated", invalidateAll),
        ws.on("squad:deleted", invalidateAll),
        // Reconnect: cheap and correct — squad identity is low-churn, one
        // refetch of the mounted squad queries covers anything missed.
        ws.onReconnect(invalidateAll),
      ];
    },
    [queryClient],
  );
}
