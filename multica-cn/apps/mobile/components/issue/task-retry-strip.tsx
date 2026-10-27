import { ActivityIndicator, Alert, Pressable, Text, View } from "react-native";
import { useRerunIssue } from "@/data/mutations/issues";
import { retryFailureMessage } from "@/lib/task-retry";
import { useT } from "@/lib/use-t";

/**
 * RUYI-343 — retry strip beneath an agent failure comment, mobile mirror of
 * web's `TaskCommentRetryButton` (comment-card.tsx): fires the issue-level
 * rerun with the comment's source task id so the right agent re-runs, not
 * the issue's current assignee. Only rendered for entries passing
 * `retryableAgentFailureComment`; the admission gate lives in
 * lib/task-retry.ts (unit-tested there). Errors surface as a native alert
 * with the same permission-vs-transient distinction as web's toast.
 *
 * RUYI-553 — extracted from comment-card.tsx and restyled from a bare
 * `text-xs` text link into a filled button (the link's tap target was too
 * small). Visual contract is class-for-class identical to run-row.tsx's
 * RetryButton — both are task-failure retries and must never drift into
 * two different button languages on one screen; issue-run-retry.test.tsx
 * pins the run-row side of that contract.
 */
export function TaskRetryStrip({
  issueId,
  taskId,
}: {
  issueId: string;
  taskId: string;
}) {
  const { t } = useT("issues");
  const mutation = useRerunIssue(issueId);

  const onPress = () => {
    if (mutation.isPending) return;
    mutation.mutate(taskId, {
      onError: (err) =>
        Alert.alert(
          t("execution_log.retry_failed", "Failed to retry task"),
          retryFailureMessage(err),
        ),
    });
  };

  return (
    <View className="flex-row items-center gap-2">
      {mutation.isPending ? (
        <ActivityIndicator size="small" />
      ) : null}
      <Pressable
        onPress={onPress}
        disabled={mutation.isPending}
        className="px-3 py-1.5 rounded-md bg-secondary active:opacity-70"
        accessibilityRole="button"
        accessibilityLabel={t(
          "execution_log.retry_task_aria",
          "Retry task",
        )}
      >
        <Text className="text-xs font-medium text-foreground">
          {t("execution_log.retry_task_tooltip", "Retry task")}
        </Text>
      </Pressable>
    </View>
  );
}
