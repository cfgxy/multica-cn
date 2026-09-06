/**
 * One active run of a specific agent, in the per-agent task list
 * (`more/agents/[id]`, RUYI-76 ②). Read-only monitoring row — cancel and
 * the run transcript stay in the issue runs sheet, where the issue context
 * lives; this row answers "what is this agent doing right now".
 *
 *   - running            → PulseDot + brand-toned "Running" semantics
 *   - queued-side states → clock glyph, muted (same tone family as RunRow)
 *   - issue-linked task  → row navigates to the issue detail
 *   - chat / autopilot / manual run (no issue) → source label, inert row
 *     (mobile has no per-session run surface to link to today)
 */
import { Pressable, View } from "react-native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type { AgentTask } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { PulseDot } from "@/components/ui/pulse-dot";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";

interface Props {
  task: AgentTask;
  /** Resolved issue title; empty when the task has no linked issue. */
  issueTitle: string | null;
  wsSlug: string | null;
}

export function AgentTaskRow({ task, issueTitle, wsSlug }: Props) {
  const { t } = useT("issues");
  const { colorScheme } = useColorScheme();
  const mutedFg = THEME[colorScheme].mutedForeground;

  const isRunning = task.status === "running";
  const hasIssue = task.issue_id !== "";
  // Same timestamp rule as RunRow: active tasks fall back to created_at so
  // the user sees how long the task has been waiting.
  const timestamp = task.started_at ?? task.created_at;

  const title = hasIssue
    ? (issueTitle ?? t("mobile.tasks.issue_unavailable", "Issue unavailable"))
    : sourceLabel(task, t);

  const body = (
    <View className="flex-row items-center gap-3 px-4 py-3">
      {isRunning ? (
        <PulseDot size={8} />
      ) : (
        <Ionicons name="time-outline" size={16} color={mutedFg} />
      )}
      <Text
        className={`flex-1 text-sm ${
          isRunning ? "text-foreground font-medium" : "text-muted-foreground"
        }`}
        numberOfLines={1}
      >
        {title}
      </Text>
      <Text className="text-xs shrink-0 text-muted-foreground">
        {timestamp ? timeAgo(timestamp) : ""}
      </Text>
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
 *  in both surfaces (shared issues-ns locale keys, no new copies). */
function sourceLabel(task: AgentTask, t: ReturnType<typeof useT>["t"]) {
  if (task.chat_session_id) {
    return t("mobile.run_summary.chat", "Chat task");
  }
  if (task.autopilot_run_id) {
    return t("mobile.run_summary.autopilot", "Autopilot run");
  }
  return t("mobile.run_summary.direct", "Task");
}
