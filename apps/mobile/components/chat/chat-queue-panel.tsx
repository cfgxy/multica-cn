import { useRef, useState } from "react";
import { Alert, Pressable, ScrollView, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { ChatPendingTask } from "@multica/core/types";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

type QueuedTask = NonNullable<ChatPendingTask["queued_tasks"]>[number];

interface Props {
  pendingTask: ChatPendingTask | undefined;
  maxHeight: number;
  canManage: boolean;
  onEdit: (task: QueuedTask) => Promise<void>;
  onRemove: (task: QueuedTask) => Promise<void>;
  onPrioritize: (task: QueuedTask) => Promise<void>;
  onClear: () => Promise<void>;
}

export function ChatQueuePanel({
  pendingTask,
  maxHeight,
  canManage,
  onEdit,
  onRemove,
  onPrioritize,
  onClear,
}: Props) {
  const tasks = pendingTask?.queued_tasks ?? [];
  const { t } = useT("chat");
  const { t: tCommon } = useT("common");
  const title = t(
    tasks.length === 1 ? "queue.title_single" : "queue.title_multiple",
    tasks.length === 1 ? "{{number}} queued message" : "{{number}} queued messages",
    { number: tasks.length },
  );
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [busyId, setBusyId] = useState<string | null>(null);
  const [expanded, setExpanded] = useState(false);
  const busyRef = useRef(false);
  if (tasks.length === 0) return null;

  const headCanRun = ["dispatched", "running", "waiting_local_directory"].includes(
    pendingTask?.status ?? "",
  );
  const run = async (taskId: string, action: () => Promise<void>) => {
    if (busyRef.current || !canManage) return;
    busyRef.current = true;
    setBusyId(taskId);
    try {
      await action();
    } finally {
      busyRef.current = false;
      setBusyId(null);
    }
  };

  return (
    <View className="mb-1 px-3">
      <View className="flex-row items-center">
        <Pressable
          onPress={() => setExpanded((value) => !value)}
          accessibilityRole="button"
          accessibilityState={{ expanded }}
          accessibilityLabel={title}
          className="min-h-8 flex-1 flex-row items-center gap-2"
        >
          <Ionicons name="list-outline" size={15} color={theme.mutedForeground} />
          <Text numberOfLines={1} className="flex-1 text-xs text-muted-foreground">
            {title}: {tasks[0]?.content?.trim() || t("queue.fallback", "Queued message")}
          </Text>
          <Ionicons name={expanded ? "chevron-down" : "chevron-up"} size={14} color={theme.mutedForeground} />
        </Pressable>
        <Pressable
          onPress={() => {
            if (busyRef.current || !canManage) return;
            Alert.alert(
              t("queue.clear_confirm_title", "Clear queued messages?"),
              t("queue.clear_confirm_body", "This removes queued messages only. The current task will keep running."),
              [
                { text: tCommon("cancel", "Cancel"), style: "cancel" },
                { text: t("queue.clear", "Clear all"), style: "destructive", onPress: () => void run("clear", onClear) },
              ],
            );
          }}
          disabled={busyId !== null || !canManage}
          accessibilityRole="button"
          accessibilityLabel={t("queue.clear", "Clear all")}
          hitSlop={8}
          className="h-8 flex-row items-center gap-1 px-2"
        >
          <Ionicons name="trash-outline" size={16} color={theme.mutedForeground} />
          <Text className="text-xs text-muted-foreground">{t("queue.clear", "Clear all")}</Text>
        </Pressable>
      </View>
      {expanded ? <ScrollView
        style={{ maxHeight: Math.max(96, maxHeight), minHeight: 0 }}
        keyboardShouldPersistTaps="handled"
      >
        {tasks.map((task, index) => {
          const disabled = busyId !== null || !canManage;
          return (
            <View key={task.task_id} className="min-h-11 flex-row items-center gap-1 border-t border-border/60 py-1">
              <Text className="w-5 text-xs text-muted-foreground">{index + 1}.</Text>
              <Text numberOfLines={2} className="mr-1 flex-1 text-sm text-foreground">
                {task.content?.trim() || t("queue.fallback", "Queued message")}
              </Text>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Pressable
                    disabled={disabled}
                    accessibilityRole="button"
                    accessibilityLabel={t("queue.more_actions", "More actions for queued message {{position}}", { position: index + 1 })}
                    accessibilityState={{ disabled, busy: busyId === task.task_id }}
                    className="h-10 w-10 items-center justify-center rounded-md"
                  >
                    <Ionicons name="ellipsis-horizontal" size={18} color={theme.mutedForeground} />
                  </Pressable>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48">
                  <DropdownMenuItem disabled={disabled || !headCanRun} onPress={() => void run(task.task_id, () => onPrioritize(task))}>
                    <Ionicons name="arrow-up-outline" size={16} color={disabled || !headCanRun ? theme.mutedForeground : theme.brand} />
                    <Text>{t("queue.steer", "Steer")}</Text>
                  </DropdownMenuItem>
                  <DropdownMenuItem disabled={disabled} onPress={() => void run(task.task_id, () => onEdit(task))}>
                    <Ionicons name="create-outline" size={16} color={theme.mutedForeground} />
                    <Text>{t("queue.edit", "Edit queued message")}</Text>
                  </DropdownMenuItem>
                  <DropdownMenuItem disabled={disabled} onPress={() => void run(task.task_id, () => onRemove(task))}>
                    <Ionicons name="close-outline" size={16} color={theme.mutedForeground} />
                    <Text>{t("queue.remove", "Remove queued message")}</Text>
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </View>
          );
        })}
      </ScrollView> : null}
    </View>
  );
}
