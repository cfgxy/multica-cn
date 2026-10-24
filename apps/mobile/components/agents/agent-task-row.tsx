/**
 * One active run of a specific agent, in the per-agent task list
 * (`more/agents/[id]`). RUYI-700 restructured the row into the three-line
 * shape both list segments now share:
 *
 *   - line 1  issue status icon + identifier + title (issue-less runs keep
 *             the source-label vocabulary)
 *   - line 2  trigger summary (direct-assign handoff note fallback); both
 *             absent → the line collapses and the row reads as two lines
 *   - line 3  run-status badge + queued reason + relative time + the avatar
 *             stack of OTHER agents active on the same issue
 *
 * Avatar-driven state (RUYI-554 unified status language, re-bucketed by
 * RUYI-700): running AND cancel_requested breathe (a stop request is
 * accepted but the task still holds the runtime); queued-side grays.
 *
 * Navigation is unchanged: issue-linked rows push the issue detail; chat /
 * autopilot / manual runs (no issue) stay inert — mobile has no
 * per-session run surface to link to today.
 */
import { Pressable, View } from "react-native";
import { router } from "expo-router";
import type { AgentTask, Issue } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { AvatarStack } from "@/components/ui/avatar-stack";
import { StatusIcon } from "@/components/ui/status-icon";
import { StatusBadge } from "@/components/issue/run-row";
import {
  agentTaskActivityState,
  type IssueOtherAgentsActivity,
  NO_OTHER_AGENTS,
} from "@/lib/issue-agent-activity";
import { issueColumnCategory } from "@/lib/issue-status";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";

interface Props {
  task: AgentTask;
  /** Resolved issue; null falls through to the short-id fallback. */
  issue: Issue | null;
  wsSlug: string | null;
  /** Other agents active on the same issue (row-3 stack). */
  otherAgents?: IssueOtherAgentsActivity;
}

export function AgentTaskRow({
  task,
  issue,
  wsSlug,
  otherAgents = NO_OTHER_AGENTS,
}: Props) {
  const { t } = useT("issues");

  const activity = agentTaskActivityState(task.status);
  const runningSide = activity === "running";
  const hasIssue = task.issue_id !== "";
  // Same timestamp rule as RunRow: active tasks fall back to created_at so
  // the user sees how long the task has been waiting.
  const timestamp = task.started_at ?? task.created_at;
  const time = timestamp ? timeAgo(timestamp) : "";

  // Line 1 — resolved issues read "IDENT title"; unresolved (beyond a list
  // window, or still fetching) mirror web's activity tab with the short-id
  // fallback, never a "task invisible" placeholder (RUYI-538 ④).
  const row1 = hasIssue
    ? (issue
        ? `${issue.identifier} ${issue.title}`
        : t("agents:tab_body.activity.issue_short_fallback", "Issue {{prefix}}…", {
            prefix: task.issue_id.slice(0, 8),
          }))
    : sourceLabel(task, t);

  // Line 2 — creation-time trigger snapshot; direct assigns fall back to
  // the assigner's handoff note. Both absent → hide the line entirely
  // (two-line form), never an empty spacer.
  const summary =
    task.trigger_summary?.trim() || task.handoff_note?.trim() || null;

  // RUYI-397 admission code, named per the RUYI-700 design (「等待运行时恢
  // 复」). Only the known code renders a hint — unknown future codes keep
  // the bare status label instead of leaking a raw enum.
  const queuedReasonHint =
    task.status !== "queued"
      ? null
      : task.queued_reason === "runtime_backpressure"
        ? t(
            "agents:mobile.run_history.queued_backpressure",
            "Waiting for runtime recovery",
          )
        : task.queued_reason === "runtime_model_call_budget"
          ? t(
              "agents:mobile.run_history.queued_model_call_budget",
              "Waiting for model call budget window",
            )
          : null;

  const runStatusA11y = t(`mobile.run_status.${task.status}`, task.status);

  const body = (
    <View
      className="flex-row items-start gap-3 px-4 py-3"
      accessibilityLabel={`${row1}, ${runStatusA11y}, ${time}`}
    >
      <ActorAvatar
        type="agent"
        id={task.agent_id}
        size={24}
        activity={activity}
      />
      <View className="flex-1 min-w-0 gap-1">
        <View className="flex-row items-center gap-1.5">
          {issue ? (
            <StatusIcon
              status={issue.status}
              category={issueColumnCategory(issue)}
              size={14}
            />
          ) : null}
          <Text
            className={`flex-1 text-sm ${
              runningSide
                ? "text-foreground font-medium"
                : "text-muted-foreground"
            }`}
            numberOfLines={1}
          >
            {row1}
          </Text>
        </View>
        {summary ? (
          <Text
            className="text-xs text-muted-foreground"
            numberOfLines={1}
          >
            {summary}
          </Text>
        ) : null}
        <View className="flex-row items-center gap-2">
          <StatusBadge task={task} />
          {queuedReasonHint ? (
            <Text
              className="text-xs text-muted-foreground"
              numberOfLines={1}
            >
              {queuedReasonHint}
            </Text>
          ) : null}
          <Text className="text-xs shrink-0 text-muted-foreground">
            {time}
          </Text>
          <View className="flex-1" />
          {otherAgents.agents.length > 0 ? (
            <View
              accessibilityLabel={t(
                "agents:mobile.run_history.other_agents",
                "{{count}} agents active",
                { count: otherAgents.agents.length },
              )}
            >
              <AvatarStack
                actors={otherAgents.agents}
                size={16}
                max={3}
                activity={otherAgents.activity ?? undefined}
              />
            </View>
          ) : null}
        </View>
      </View>
    </View>
  );

  if (!hasIssue || !wsSlug) return body;

  return (
    <Pressable
      onPress={() => {
        router.push(`/${wsSlug}/issue/${task.issue_id}`);
      }}
      className="bg-background active:bg-secondary"
    >
      {body}
    </Pressable>
  );
}

/** Same wording as RunRow's run-summary fallbacks so a task reads the same
 *  in both surfaces (shared issues-ns locale keys, no new copies). Exported
 *  for the run-history row (RUYI-538 ②), which shares the issue-less
 *  fallback vocabulary. */
export function sourceLabel(task: AgentTask, t: ReturnType<typeof useT>["t"]) {
  if (task.chat_session_id) {
    return t("mobile.run_summary.chat", "Chat task");
  }
  if (task.autopilot_run_id) {
    return t("mobile.run_summary.autopilot", "Autopilot run");
  }
  return t("mobile.run_summary.direct", "Task");
}
