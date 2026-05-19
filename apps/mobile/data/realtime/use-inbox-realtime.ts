/**
 * Inbox realtime — Layer 3 of the realtime stack.
 *
 * Listens for inbox-domain events on the shared WSClient and invalidates
 * the inbox query so TanStack Query refetches. Also re-invalidates on
 * reconnect because we may have missed events while disconnected (no
 * server-side replay in v1; that's a future optimization on top of the
 * Redis relay buffer the server already maintains).
 *
 * Web's equivalent does fancier per-event work (specific handlers that
 * mutate the cache without a refetch, OS notifications on inbox:new,
 * etc — see packages/core/realtime/use-realtime-sync.ts). Mobile v1
 * starts with the simplest correct path: invalidate-and-refetch. Push
 * notifications for backgrounded delivery come later via APNs, not WS.
 */
import { useQueryClient } from "@tanstack/react-query";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";

export function useInboxRealtime() {
  const queryClient = useQueryClient();

  useWSSubscriptions(
    (ws, wsId) => {
      // Same key shape as data/queries/inbox.ts → inboxListOptions(wsId).
      // Keying on wsId means workspace switches naturally invalidate.
      const invalidate = () => {
        queryClient.invalidateQueries({ queryKey: ["inbox", wsId] });
      };
      return [
        ws.on("inbox:new", invalidate),
        ws.on("inbox:read", invalidate),
        ws.on("inbox:archived", invalidate),
        ws.on("inbox:batch-read", invalidate),
        ws.on("inbox:batch-archived", invalidate),
        // After a reconnect we don't know what we missed during the
        // downtime — refresh from server.
        ws.onReconnect(invalidate),
      ];
    },
    [queryClient],
  );
}
