/**
 * One-line presence summary for an agent: availability dot + label,
 * workload label, and live counts. Mobile mirror of web's
 * `AgentPresenceIndicator` non-compact form
 * (packages/views/agents/components/agent-presence-indicator.tsx) —
 * same dimensions (availability / workload), same tone rules:
 *
 *   - dot colour reads ONLY from availability (3 states + archived)
 *   - workload shows a labelled `running / capacity` ratio when working,
 *     bare queued count when queued-only, plus the queue badge when tasks
 *     are waiting behind a running one (RUYI-418 B4, web parity)
 *   - a queued label on a healthy (online) runtime composes down to muted —
 *     amber there is the offline-runtime stuck signal, not a transient race
 *   - archived agents skip the workload segment ("Archived" says it all)
 *
 * The ratio never folds resource_weight in: it is running tasks against the
 * scheduler cap (max_concurrent_tasks), matching web and the daemon.
 *
 * Presentational: the caller passes the already-derived
 * `AgentPresenceDetail` (from `useWorkspacePresenceMap` for lists or
 * `useAgentPresence` for a single agent) — same prop contract as web.
 */
import { View } from "react-native";
import type { AgentPresenceDetail } from "@multica/core/agents";
import { Text } from "@/components/ui/text";
import { PresenceDot } from "@/components/ui/presence-dot";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

const AVAILABILITY_TEXT: Record<AgentPresenceDetail["availability"], string> = {
  online: "text-success",
  unstable: "text-warning",
  offline: "text-muted-foreground",
  archived: "text-muted-foreground",
};

const WORKLOAD_TEXT: Record<AgentPresenceDetail["workload"], string> = {
  working: "text-brand",
  queued: "text-warning",
  idle: "text-muted-foreground",
};

export function AgentPresenceLine({
  detail,
  className,
}: {
  detail: AgentPresenceDetail;
  className?: string;
}) {
  const { t } = useT("agents");

  const availabilityLabel = t(`availability.${detail.availability}`, {
    // 已知枚举直接给英文兜底；未知值透出原值（API Response Compatibility）。
    defaultValue: detail.availability,
  });
  const workloadLabel = t(`workload.${detail.workload}`, {
    defaultValue: detail.workload,
  });
  const isWorking = detail.workload === "working";
  const isQueued = detail.workload === "queued";
  const showWorkload = detail.availability !== "archived";
  const showQueueBadge = isWorking && detail.queuedCount > 0;
  const queuedMuted = detail.availability === "online";

  return (
    <View className={cn("flex-row flex-wrap items-center gap-1.5", className)}>
      <PresenceDot availability={detail.availability} size={7} />
      <Text className={cn("text-xs", AVAILABILITY_TEXT[detail.availability])}>
        {availabilityLabel}
      </Text>
      {showWorkload ? (
        <>
          <Text className="text-xs text-muted-foreground">·</Text>
          <Text
            className={cn(
              "text-xs",
              isQueued && queuedMuted
                ? "text-muted-foreground"
                : WORKLOAD_TEXT[detail.workload],
            )}
          >
            {workloadLabel}
          </Text>
          {isWorking ? (
            <Text className="text-xs tabular-nums text-muted-foreground">
              {t("presence.running_ratio", {
                defaultValue: "{{running}}/{{capacity}} running",
                running: detail.runningCount,
                capacity: detail.capacity,
              })}
            </Text>
          ) : null}
          {showQueueBadge ? (
            <View className="rounded bg-muted-foreground/10 px-1 py-0.5">
              <Text className="text-[10px] font-medium text-muted-foreground">
                {t("presence.queue_badge", {
                  defaultValue: "{{count}} queued",
                  count: detail.queuedCount,
                })}
              </Text>
            </View>
          ) : null}
          {isQueued ? (
            <Text className="text-xs tabular-nums text-muted-foreground">
              {detail.queuedCount}
            </Text>
          ) : null}
        </>
      ) : null}
    </View>
  );
}
