/**
 * Compact "an agent is on this issue" badge for dense rows (inbox now,
 * issue rows later). Mirrors web's `IssueAgentActivityIndicator`
 * (packages/views/issues/components/issue-agent-activity-indicator.tsx) in
 * its badge-only, no-hover-card form — the exact variant web's inbox rows
 * render (MUL-5189):
 *
 *   ≥1 running task          → avatar stack + PulseDot          (full)
 *   0 running, ≥1 queued     → half-opacity stack, no dot
 *   nothing                  → null (no chrome, no placeholder)
 *
 * Stack heads prefer running and fall back to queued — never both — same
 * as web. Purely presentational: the caller sources the task groups from
 * the shared workspace snapshot (`lib/issue-agent-activity.ts`), so long
 * lists pay one derivation pass, not one per row.
 *
 * Documented divergence from web: the web badge also renders a
 * "Working"/"Queued" text label; mobile drops the label for row width —
 * the same call `AgentHeaderBadge` made — and the running/queued
 * distinction reads from the PulseDot vs the half-opacity stack.
 */
import { View } from "react-native";
import { useT } from "@/lib/use-t";
import type { AgentTask } from "@multica/core/types";
import { AvatarStack, type StackActor } from "@/components/ui/avatar-stack";
import { PulseDot } from "@/components/ui/pulse-dot";

interface Props {
  running: AgentTask[];
  queued: AgentTask[];
  /** Avatar diameter in pt. 16 keeps the badge inside a dense row. */
  size?: number;
}

export function IssueAgentActivityBadge({ running, queued, size = 16 }: Props) {
  const { t } = useT("issues");
  const primary = running.length > 0 ? running : queued;
  if (primary.length === 0) return null;

  const isRunning = running.length > 0;
  const actors = primary.map<StackActor>((task) => ({
    type: "agent",
    id: task.agent_id,
  }));

  return (
    <View
      className="flex-row items-center"
      style={isRunning ? undefined : { opacity: 0.5 }}
      accessibilityLabel={
        isRunning
          ? t(
              "mobile.agent_activity.row_running_a11y",
              "Agent working on this issue",
            )
          : t(
              "mobile.agent_activity.row_queued_a11y",
              "Agent task queued for this issue",
            )
      }
    >
      <AvatarStack actors={actors} max={2} size={size} />
      {isRunning ? (
        <View className="ml-1">
          <PulseDot size={6} />
        </View>
      ) : null}
    </View>
  );
}
