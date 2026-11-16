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
 * RUYI-864: the decisions tab's authorization queue is a server-ordered single
 * card (oldest pending, order=asc, limit=1) plus the collapsed history. When a
 * request is answered from another client the queue must advance and the
 * history refresh, so this hook also invalidates the request subtree on
 * `decision_request:updated` — the exact mirror of web's
 * `decisionRequestKeys.all()` invalidation in use-realtime-sync.ts. The list
 * is tiny, so a subtree invalidation is cheaper than patching.
 *
 * Reconnect: we may have missed events while down — refetch from server.
 */
import { useQueryClient } from "@tanstack/react-query";
import { decisionInboxKeys } from "@multica/core/issues/decisions";
import { decisionRequestKeys } from "@multica/core/issues/decision-requests";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";

export function useDecisionInboxRealtime() {
  const qc = useQueryClient();

  useWSSubscriptions(
    (ws) => {
      const invalidateInbox = () => {
        qc.invalidateQueries({ queryKey: decisionInboxKeys.all() });
      };
      const invalidateRequests = () => {
        qc.invalidateQueries({ queryKey: decisionRequestKeys.all() });
      };
      return [
        ws.on("decision:updated", invalidateInbox),
        ws.on("decision_request:updated", invalidateRequests),
        ws.onReconnect(() => {
          invalidateInbox();
          invalidateRequests();
        }),
      ];
    },
    [qc],
  );
}
