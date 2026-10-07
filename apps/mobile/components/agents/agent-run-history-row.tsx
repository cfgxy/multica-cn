/**
 * One terminal run of a specific agent, in the agent run-history section
 * (RUYI-538 ② — completed / failed / cancelled, newest first). Mobile
 * mirror of web's agent activity-tab history rows:
 *
 *   - title: linked issue (title, or the short-id fallback while
 *     unresolved) / issue-less runs keep the source-label vocabulary of the
 *     active rows above;
 *   - status: the same StatusBadge as the issue runs sheet — failed rows
 *     inline the failure_reason, one vocabulary everywhere;
 *   - retry: failed retries with one tap, cancelled goes through a "Run
 *     again" confirm (same split as RunRow / web's execution log). Retries
 *     target this row's task id on its source issue via the run-level
 *     endpoint (RUYI-292);
 *   - tap: linked issues push the run detail sheet; issue-less rows are
 *     inert (no per-session surface on mobile today, same as active rows).
 */
import { Alert, Pressable, View } from "react-native";
import { router } from "expo-router";
import type { AgentTask } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { StatusBadge } from "@/components/issue/run-row";
import { useRetryAgentRun } from "@/data/mutations/agents";
import { isAgentRunRetryable } from "@/lib/agent-run-history";
import { retryFailureMessage } from "@/lib/task-retry";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";
import { sourceLabel } from "./agent-task-row";

interface Props {
  task: AgentTask;
  /** Resolved issue title; null falls through to the short-id fallback. */
  issueTitle: string | null;
  wsSlug: string | null;
}

export function AgentRunHistoryRow({ task, issueTitle, wsSlug }: Props) {
  const { t } = useT("issues");

  const hasIssue = task.issue_id !== "";
  const canRetry = isAgentRunRetryable(task);
  const timestamp = task.completed_at;

  const title = hasIssue
    ? (issueTitle ??
      t("agents:tab_body.activity.issue_short_fallback", "Task {{prefix}}...", {
        prefix: task.issue_id.slice(0, 8),
      }))
    : sourceLabel(task, t);

  return (
    <Pressable
      disabled={!hasIssue || !wsSlug}
      onPress={() => {
        router.push({
          pathname: "/[workspace]/issue/[id]/runs/[taskId]",
          params: { workspace: wsSlug ?? "", id: task.issue_id, taskId: task.id },
        });
      }}
      className={`flex-row items-start gap-3 px-4 py-2.5 ${
        hasIssue && wsSlug ? "active:bg-secondary" : ""
      }`}
    >
      <View className="flex-1 gap-1 min-w-0">
        <Text className="text-sm text-foreground" numberOfLines={2}>
          {title}
        </Text>
        <View className="flex-row items-center gap-2">
          <StatusBadge task={task} />
          <Text className="text-xs text-muted-foreground">
            {timestamp ? timeAgo(timestamp) : ""}
          </Text>
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
