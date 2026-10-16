import { useState } from "react";
import { Pressable, ScrollView, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { ChatPendingTask } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

type QueuedTask = NonNullable<ChatPendingTask["queued_tasks"]>[number];

interface Props {
  pendingTask: ChatPendingTask | undefined;
  canPrioritize: boolean;
  onEdit: (task: QueuedTask) => Promise<void>;
  onRemove: (task: QueuedTask) => Promise<void>;
  onPrioritize: (task: QueuedTask) => Promise<void>;
  onClear: () => void;
}

export function ChatQueuePanel({
  pendingTask,
  canPrioritize,
  onEdit,
  onRemove,
  onPrioritize,
  onClear,
}: Props) {
  const tasks = pendingTask?.queued_tasks ?? [];
  const { t } = useT("chat");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [busyId, setBusyId] = useState<string | null>(null);
  if (tasks.length === 0) return null;

  const headCanRun = ["dispatched", "running", "waiting_local_directory"].includes(
    pendingTask?.status ?? "",
  );
  const run = async (taskId: string, action: () => Promise<void>) => {
    if (busyId) return;
    setBusyId(taskId);
    try {
      await action();
    } finally {
      setBusyId(null);
    }
  };

  return (
    <View className="mb-2 rounded-lg border border-border bg-background px-3 py-2" accessibilityLabel={t("queue.title", "Queued messages")}>
      <View className="mb-1 flex-row items-center justify-between">
        <Text className="font-medium text-foreground">
          {t("queue.title", "Queue")} ({tasks.length})
        </Text>
        <Pressable
          onPress={onClear}
          disabled={busyId !== null}
          accessibilityRole="button"
          accessibilityLabel={t("queue.clear", "Clear queue")}
          hitSlop={8}
          className="h-9 flex-row items-center gap-1 px-2"
        >
          <Ionicons name="trash-outline" size={16} color={theme.mutedForeground} />
          <Text className="text-xs text-muted-foreground">{t("queue.clear", "Clear")}</Text>
        </Pressable>
      </View>
      <ScrollView className="max-h-40" keyboardShouldPersistTaps="handled">
        {tasks.map((task, index) => {
          const disabled = busyId !== null;
          const buttonStyle = "h-10 min-w-10 items-center justify-center rounded-md px-2";
          return (
            <View key={task.task_id} className="min-h-11 flex-row items-center gap-1 border-t border-border/60 py-1">
              <Text className="w-5 text-xs text-muted-foreground">{index + 1}.</Text>
              <Text numberOfLines={2} className="mr-1 flex-1 text-sm text-foreground">
                {task.content?.trim() || t("queue.fallback", "Queued message")}
              </Text>
              <Pressable
                onPress={() => void run(task.task_id, () => onPrioritize(task))}
                disabled={disabled || !headCanRun || !canPrioritize}
                accessibilityRole="button"
                accessibilityLabel={t("queue.steer", "Send now")}
                className={buttonStyle}
              >
                <Ionicons name="arrow-up-outline" size={18} color={disabled || !headCanRun || !canPrioritize ? theme.mutedForeground : theme.brand} />
              </Pressable>
              <Pressable
                onPress={() => void run(task.task_id, () => onEdit(task))}
                disabled={disabled}
                accessibilityRole="button"
                accessibilityLabel={t("queue.edit", "Edit queued message")}
                className={buttonStyle}
              >
                <Ionicons name="create-outline" size={18} color={theme.mutedForeground} />
              </Pressable>
              <Pressable
                onPress={() => void run(task.task_id, () => onRemove(task))}
                disabled={disabled}
                accessibilityRole="button"
                accessibilityLabel={t("queue.remove", "Remove queued message")}
                className={buttonStyle}
              >
                <Ionicons name="close-outline" size={18} color={theme.mutedForeground} />
              </Pressable>
            </View>
          );
        })}
      </ScrollView>
    </View>
  );
}
