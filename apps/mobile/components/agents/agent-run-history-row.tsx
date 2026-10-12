/**
 * One terminal run of a specific agent, in the agent run-history section
 * (RUYI-538 ② — completed / failed / cancelled, newest first). RUYI-700
 * made the row isomorphic with the active rows above — same three-line
 * shape, static avatar (terminal runs don't breathe):
 *
 *   - line 1  issue status icon + identifier + title (issue-less runs keep
 *             the source-label vocabulary of the active rows);
 *   - line 2  trigger summary / handoff note, hidden when both absent;
 *   - line 3  status badge — failed rows inline the failure_reason, one
 *             vocabulary everywhere — plus relative time and the avatar
 *             stack of other agents still active on the same issue.
 *
 * Interactions unchanged: failed retries with one tap, cancelled goes
 * through a "Run again" confirm (same split as RunRow / web's execution
 * log; retries target this row's task id on its source issue via the
 * run-level endpoint, RUYI-292); linked issues push the run detail sheet,
 * issue-less rows are inert.
 */
import { Alert, Pressable, View } from "react-native";
import { router } from "expo-router";
import type { AgentTask, Issue } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { AvatarStack } from "@/components/ui/avatar-stack";
import { StatusIcon } from "@/components/ui/status-icon";
import { StatusBadge } from "@/components/issue/run-row";
import {
  type IssueOtherAgentsActivity,
  NO_OTHER_AGENTS,
} from "@/lib/issue-agent-activity";
import { issueColumnCategory } from "@/lib/issue-status";
import { useRetryAgentRun } from "@/data/mutations/agents";
import { isAgentRunRetryable } from "@/lib/agent-run-history";
import { retryFailureMessage } from "@/lib/task-retry";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";
import { sourceLabel } from "./agent-task-row";

interface Props {
  task: AgentTask;
  /** Resolved issue; null falls through to the short-id fallback. */
  issue: Issue | null;
  wsSlug: string | null;
  /** Other agents still active on the same issue (row-3 stack). */
  otherAgents?: IssueOtherAgentsActivity;
}

export function AgentRunHistoryRow({
  task,
  issue,
  wsSlug,
  otherAgents = NO_OTHER_AGENTS,
}: Props) {
  const { t } = useT("issues");

  const hasIssue = task.issue_id !== "";
  const canRetry = isAgentRunRetryable(task);
  const timestamp = task.completed_at;
  const time = timestamp ? timeAgo(timestamp) : "";

  const row1 = hasIssue
    ? (issue
        ? `${issue.identifier} ${issue.title}`
        : t("agents:tab_body.activity.issue_short_fallback", "Issue {{prefix}}…", {
            prefix: task.issue_id.slice(0, 8),
          }))
    : sourceLabel(task, t);

  const summary =
    task.trigger_summary?.trim() || task.handoff_note?.trim() || null;

  return (
    <Pressable
      disabled={!hasIssue || !wsSlug}
      onPress={() => {
        router.push({
          pathname: "/[workspace]/issue/[id]/runs/[taskId]",
          params: { workspace: wsSlug ?? "", id: task.issue_id, taskId: task.id },
        });
      }}
      className={`flex-row items-start gap-3 px-4 py-3 ${
        hasIssue && wsSlug ? "active:bg-secondary" : ""
      }`}
    >
      <ActorAvatar type="agent" id={task.agent_id} size={24} />
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
            className="flex-1 text-sm text-foreground"
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
      {canRetry ? <RetryButton task={task} /> : null}
    </Pressable>
  );
}

// Same failed-direct / cancelled-confirm split as the issue runs sheet's
// RetryButton — a cancelled run was stopped on purpose, so re-running it
// asks first and reads as "Run again".
function RetryButton({ task }: { task: AgentTask }) {
  const { t } = useT("issues");
  const retry = useRetryAgentRun();
  const isRerun = task.status === "cancelled";

  const onPress = () => {
    if (retry.isPending) return;
    const issueId = task.issue_id;
    const fire = () => {
      retry.mutate(
        { issueId, taskId: task.id },
        {
          onError: (err) =>
            Alert.alert(
              t("execution_log.retry_failed", "Failed to retry task"),
              retryFailureMessage(err),
            ),
        },
      );
    };
    if (isRerun) {
      Alert.alert(
        t("execution_log.rerun_task_tooltip", "Run again"),
        undefined,
        [
          {
            text: t("terminate_dialog.keep", "Keep running"),
            style: "cancel",
          },
          {
            text: t("execution_log.rerun_task_tooltip", "Run again"),
            style: "destructive",
            onPress: fire,
          },
        ],
      );
      return;
    }
    fire();
  };

  return (
    <Pressable
      onPress={onPress}
      disabled={retry.isPending}
      className="px-3 py-1.5 rounded-md bg-secondary active:opacity-70 self-center"
      accessibilityRole="button"
      accessibilityLabel={
        isRerun
          ? t("execution_log.rerun_task_aria", "Run again")
          : t("execution_log.retry_task_aria", "Retry task")
      }
    >
      <Text className="text-xs font-medium text-foreground">
        {isRerun
          ? t("execution_log.rerun_task_tooltip", "Run again")
          : t("execution_log.retry_task_tooltip", "Retry task")}
      </Text>
    </Pressable>
  );
}
