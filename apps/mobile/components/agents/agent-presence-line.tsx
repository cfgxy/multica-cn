/**
 * One-line presence summary for an agent: availability dot + label,
 * workload label, and live counts. Mobile mirror of web's
 * `AgentPresenceIndicator` non-compact form
 * (packages/views/agents/components/agent-presence-indicator.tsx) —
 * same dimensions (availability / workload), same tone rules:
 *
 *   - dot colour reads ONLY from availability (3 states + archived)
 *   - workload shows counts: `running / capacity` when working, bare
 *     queued count when queued-only
 *   - archived agents skip the workload segment ("Archived" says it all)
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

  return (
    <View className={cn("flex-row items-center gap-1.5", className)}>
      <PresenceDot availability={detail.availability} size={7} />
      <Text className={cn("text-xs", AVAILABILITY_TEXT[detail.availability])}>
        {availabilityLabel}
      </Text>
      {showWorkload ? (
        <>
          <Text className="text-xs text-muted-foreground">·</Text>
          <Text className={cn("text-xs", WORKLOAD_TEXT[detail.workload])}>
            {workloadLabel}
          </Text>
          {isWorking ? (
            <Text className="text-xs tabular-nums text-muted-foreground">
              {detail.runningCount} / {detail.capacity}
            </Text>
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
