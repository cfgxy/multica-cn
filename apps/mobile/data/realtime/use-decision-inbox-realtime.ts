/**
 * Decision-inbox realtime (RUYI-494) — Layer 3 of the realtime stack.
 *
 * `decision:updated` fires on every card answer/cancel (CAS winner included),
 * so refetching the small workspace aggregation is cheaper than patching it:
 * the server recomputes `counts.open` and the answered card's section move in
 * one response. Web does the same via `decisionInboxKeys.all()` invalidation
 * in packages/core/realtime/use-realtime-sync.ts — mirrored here, not
 * skipped (behavioral parity: badge and list must agree across clients).
 *
 * Per-issue card patching stays in use-issue-realtime.ts (`upsertDecisionInCache`
 * on the per-issue keys); this hook only owns the workspace-level aggregate.
 *
 * Reconnect: we may have missed events while down — refetch from server.
 */
import { useQueryClient } from "@tanstack/react-query";
import { decisionInboxKeys } from "@multica/core/issues/decisions";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";

export function useDecisionInboxRealtime() {
  const qc = useQueryClient();

  useWSSubscriptions(
    (ws) => {
      const invalidate = () => {
        qc.invalidateQueries({ queryKey: decisionInboxKeys.all() });
      };
      return [ws.on("decision:updated", invalidate), ws.onReconnect(invalidate)];
    },
    [qc],
  );
}
