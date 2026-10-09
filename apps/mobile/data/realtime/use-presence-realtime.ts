/**
 * Presence realtime — Layer 3 of the realtime stack. Listing-level (always
 * on while the user is inside a workspace).
 *
 * Invalidates the queries that back the presence dot (keys via the
 * data/queries/* factories — RUYI-346 migrated the inline 2-segment keys
 * here to the 3-segment factories):
 *   - runtimeListOptions      ← daemon:register, runtime sweeper transitions
 *   - agentListOptions        ← agent:status / created / archived / restored
 *   - agentTaskSnapshotOptions← task:queued / dispatch / completed / failed /
 *                               cancelled
 *
 * Agents invalidates `agentKeys.all` (not just the list): since RUYI-346 the
 * detail caches exist, and a full Agent payload rides these events — the
 * detail screen's presence/status stays honest the same way the list does.
 *
 * Deliberately NOT subscribed (cellular-data rule, apps/mobile/CLAUDE.md):
 *   - daemon:heartbeat — every 15s × in-online runtime; web also skips it
 *     (packages/core/realtime/use-realtime-sync.ts:147). An invalidate per
 *     heartbeat would refetch agents+runtimes+snapshot 4× a minute per
 *     online runtime — guaranteed to wedge the user on cellular.
 *   - task:progress / task:message — fire many times per active task. The
 *     presence cache only needs lifecycle transitions, not per-step updates.
 *
 * Reconnect: re-invalidate runtimes + snapshot (NOT agents — agent identity
 * doesn't drift while we're offline; runtime status and task counts do).
 */
import { useQueryClient } from "@tanstack/react-query";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";
import { agentKeys } from "@/data/queries/agents";
import { runtimeKeys } from "@/data/queries/runtimes";
import { agentTaskSnapshotKeys } from "@/data/queries/agent-task-snapshot";
import { agentTasksKeys } from "@/data/queries/agent-tasks";

export function usePresenceRealtime() {
  const queryClient = useQueryClient();

  useWSSubscriptions(
    (ws, wsId) => {
      const runtimesKey = runtimeKeys.all(wsId);
      const agentsKey = agentKeys.all(wsId);
      const snapshotKey = agentTaskSnapshotKeys.all(wsId);
      const tasksKey = agentTasksKeys.all(wsId);

      const invalidateRuntimes = () =>
        queryClient.invalidateQueries({ queryKey: runtimesKey });
      const invalidateAgents = () =>
        queryClient.invalidateQueries({ queryKey: agentsKey });
      // Snapshot (presence counters) + full per-agent task list (RUYI-538 ②
      // run history) move on the same lifecycle events, so one handler
      // invalidates both.
      const invalidateTaskData = () => {
        queryClient.invalidateQueries({ queryKey: snapshotKey });
        queryClient.invalidateQueries({ queryKey: tasksKey });
      };

      return [
        // Daemon lifecycle — register events mean a runtime came online or
        // re-registered; the sweeper's offline transitions are NOT pushed as
        // a WS event, but the next agent:status / task:* event will pull a
        // fresh runtime list anyway, and the 30s wall-clock tick masks the
        // gap. Heartbeats deliberately omitted.
        ws.on("daemon:register", invalidateRuntimes),

        // Agent identity churn — visible in pickers / chat header straight
        // away, so invalidate the cached list (and any open detail).
        ws.on("agent:status", invalidateAgents),
        ws.on("agent:created", invalidateAgents),
        ws.on("agent:archived", invalidateAgents),
        ws.on("agent:restored", invalidateAgents),

        // Task lifecycle — drives the workload dimension of presence and the
        // reserved-for-P1 peek sheet. progress / message intentionally absent.
        ws.on("task:queued", invalidateTaskData),
        ws.on("task:dispatch", invalidateTaskData),
        ws.on("task:completed", invalidateTaskData),
        ws.on("task:failed", invalidateTaskData),
        ws.on("task:cancelled", invalidateTaskData),

        // We may have missed sweeper-driven runtime offline transitions
        // while disconnected — refetch runtimes + snapshot. Agents not
        // re-invalidated because agent:created / archived are rare enough
        // that the user can pull-to-refresh if needed.
        ws.onReconnect(() => {
          invalidateRuntimes();
          invalidateTaskData();
        }),
      ];
    },
    [queryClient],
  );
}
