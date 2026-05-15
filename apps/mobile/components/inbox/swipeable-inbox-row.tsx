/**
 * Left-swipe-to-archive wrapper for inbox rows.
 *
 * iOS pattern reference: Mail.app / Linear iOS / Things — a destructive
 * red Archive action revealed by a leftward drag, with auto-trigger on
 * full swipe past threshold so the user doesn't have to release-then-tap.
 *
 * Why ReanimatedSwipeable (not the legacy Swipeable): RNGH 2.20+ ships the
 * Reanimated-driven implementation that integrates cleanly with the
 * existing reanimated@4 install and runs the swipe on the UI thread (the
 * legacy version uses Animated, which jankcs on heavy lists). The
 * gesture-handler root is already mounted in apps/mobile/app/_layout.tsx.
 *
 * Behaviour notes:
 *   - `friction=2` slightly slows the drag so the action doesn't fire by
 *     accident on a fast vertical scroll that catches some horizontal motion.
 *   - `rightThreshold=80` matches the visual width of the Archive button:
 *     past that, releasing auto-fires `onArchive`.
 *   - `onSwipeableOpen("right")` is the auto-trigger path. RNGH names the
 *     direction by where the action ENDS UP visible — `right` means
 *     `renderRightActions` is now exposed (the row slid left). Counter-
 *     intuitive but documented.
 *   - We `swipeable.close()` BEFORE calling onArchive so the row's exit
 *     from the FlatList (driven by the optimistic mutation flipping
 *     `archived: true`, which the parent's `deduplicateInboxItems` filters
 *     out) doesn't race the open animation. The row simply disappears from
 *     the list on next render — no fancy collapse needed for v1.
 *   - Tap on the revealed button is also supported: same flow.
 */
import { useRef } from "react";
import { Pressable, View } from "react-native";
import Animated, { type SharedValue } from "react-native-reanimated";
import ReanimatedSwipeable, {
  type SwipeableMethods,
} from "react-native-gesture-handler/ReanimatedSwipeable";
import { Ionicons } from "@expo/vector-icons";
import type { InboxItem } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { InboxRow } from "./inbox-row";

const ACTION_WIDTH = 80;

interface Props {
  item: InboxItem;
  onPress: () => void;
  onArchive: () => void;
}

export function SwipeableInboxRow({ item, onPress, onArchive }: Props) {
  const ref = useRef<SwipeableMethods>(null);

  const fireArchive = () => {
    // Close first so the swipe spring doesn't fight the row's removal from
    // FlatList on the next render tick.
    ref.current?.close();
    onArchive();
  };

  return (
    <ReanimatedSwipeable
      ref={ref}
      friction={2}
      rightThreshold={ACTION_WIDTH}
      renderRightActions={(_progress, _drag) => (
        <ArchiveAction onPress={fireArchive} drag={_drag} />
      )}
      onSwipeableOpen={(direction) => {
        if (direction === "right") fireArchive();
      }}
    >
      <InboxRow item={item} onPress={onPress} />
    </ReanimatedSwipeable>
  );
}

// `drag` is a SharedValue that goes from 0 → -ACTION_WIDTH as the row slides
// left. We could use it to drive a parallax effect on the icon; v1 just
// pins the icon to the right edge — sufficient for the intent.
function ArchiveAction({
  onPress,
  drag: _drag,
}: {
  onPress: () => void;
  drag: SharedValue<number>;
}) {
  return (
    <Animated.View style={{ width: ACTION_WIDTH }}>
      <Pressable
        onPress={onPress}
        accessibilityLabel="Archive"
        className="flex-1 items-center justify-center bg-destructive"
      >
        <View className="items-center gap-0.5">
          <Ionicons name="archive-outline" size={20} color="white" />
          <Text className="text-xs text-white">Archive</Text>
        </View>
      </Pressable>
    </Animated.View>
  );
}
