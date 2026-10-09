/**
 * Left-swipe-to-reveal-Cancel wrapper for a per-agent active task row
 * (RUYI-538 ③ — replaces the batch 「取消全部」 button, whose confirm
 * dialog couldn't tell you WHICH task died and invited mis-taps).
 *
 * Same interaction contract as the inbox archive row
 * (`components/inbox/swipeable-inbox-row.tsx`): reveal-only, no auto-fire —
 * the two-step gesture (drag past the detent, then an explicit tap on the
 * red action) is the mis-tap protection, so no extra Alert on top. Web
 * cancels without a confirm dialog too. The fire path is strictly per-task:
 * onCancel carries THIS row's task id (`api.cancelTaskById` via the
 * parent's useCancelAgentTask), so a swipe on row A can only ever send A's
 * id — neighbours are unreachable from here (asserted in the jest test).
 */
import { useRef } from "react";
import { Pressable, View } from "react-native";
import Animated, {
  type SharedValue,
  useAnimatedReaction,
  runOnJS,
} from "react-native-reanimated";
import ReanimatedSwipeable, {
  type SwipeableMethods,
} from "react-native-gesture-handler/ReanimatedSwipeable";
import { Ionicons } from "@expo/vector-icons";
import * as Haptics from "expo-haptics";
import type { AgentTask } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";
import { AgentTaskRow } from "./agent-task-row";

const ACTION_WIDTH = 80;

interface Props {
  task: AgentTask;
  /** Resolved issue title; null falls through to the short-id fallback. */
  issueTitle: string | null;
  wsSlug: string | null;
  /** False renders the plain row — swipe is a manager+active-task affordance. */
  cancellable: boolean;
  /** Fire the per-task cancel (the row closes itself first). */
  onCancel: () => void;
}

export function SwipeableAgentTaskRow({
  task,
  issueTitle,
  wsSlug,
  cancellable,
  onCancel,
}: Props) {
  const ref = useRef<SwipeableMethods>(null);
  const { t } = useT("issues");

  const fireCancel = () => {
    // Close first so the spring doesn't fight the row's status flip (the
    // settle invalidate moves it to the terminal side of the list).
    ref.current?.close();
    onCancel();
  };

  if (!cancellable) {
    return <AgentTaskRow task={task} issueTitle={issueTitle} wsSlug={wsSlug} />;
  }

  return (
    <ReanimatedSwipeable
      ref={ref}
      friction={2}
      rightThreshold={ACTION_WIDTH}
      renderRightActions={(_progress, drag) => (
        <CancelAction onPress={fireCancel} drag={drag} t={t} />
      )}
    >
      <AgentTaskRow task={task} issueTitle={issueTitle} wsSlug={wsSlug} />
    </ReanimatedSwipeable>
  );
}

function CancelAction({
  onPress,
  drag,
  t,
}: {
  onPress: () => void;
  drag: SharedValue<number>;
  t: ReturnType<typeof useT>["t"];
}) {
  // One-shot haptic when the drag crosses the action width threshold —
  // bridged from the UI thread like the inbox row.
  useAnimatedReaction(
    () => drag.value <= -ACTION_WIDTH,
    (crossed, prev) => {
      if (crossed && !prev) {
        runOnJS(Haptics.impactAsync)(Haptics.ImpactFeedbackStyle.Medium);
      }
    },
    [],
  );

  return (
    <Animated.View style={{ width: ACTION_WIDTH }}>
      <Pressable
        onPress={onPress}
        accessibilityRole="button"
        accessibilityLabel={t(
          "execution_log.cancel_task_tooltip",
          "Cancel task",
        )}
        className="flex-1 items-center justify-center bg-destructive"
      >
        <View className="items-center gap-0.5">
          <Ionicons name="stop-circle-outline" size={20} color="white" />
          <Text className="text-xs text-white">
            {t("execution_log.cancel_task_tooltip", "Cancel task")}
          </Text>
        </View>
      </Pressable>
    </Animated.View>
  );
}
