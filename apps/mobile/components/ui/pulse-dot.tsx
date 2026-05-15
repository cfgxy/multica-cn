/**
 * Slow green pulse — opacity oscillation on the UI thread via Reanimated's
 * `withRepeat`. 2-second cycle (1s in + 1s out). Same animation library as
 * comment-card.tsx, no new primitive.
 *
 * Used by:
 *   - apps/mobile/components/issue/agent-activity-row.tsx (in-card "Working" row)
 *   - apps/mobile/components/issue/agent-header-badge.tsx (Stack header ambient badge)
 *
 * Color is `#22c55e // success` (mobile tailwind.config.js:50). Inline hex
 * is the project's convention for animated backgroundColor values that
 * can't go through NativeWind className (see Reanimated style merging).
 */
import { useEffect } from "react";
import Animated, {
  useAnimatedStyle,
  useSharedValue,
  withRepeat,
  withTiming,
} from "react-native-reanimated";

interface Props {
  /** Diameter in pt. Default 8 (matches the in-card row). */
  size?: number;
}

export function PulseDot({ size = 8 }: Props) {
  const opacity = useSharedValue(0.3);
  useEffect(() => {
    opacity.value = withRepeat(
      withTiming(1, { duration: 1000 }),
      -1, // infinite
      true, // reverse — yields 0.3 ↔ 1.0 oscillation over 2s
    );
  }, [opacity]);

  const style = useAnimatedStyle(() => ({ opacity: opacity.value }));

  return (
    <Animated.View
      style={[
        {
          width: size,
          height: size,
          borderRadius: size / 2,
          backgroundColor: "#22c55e", // success
        },
        style,
      ]}
    />
  );
}
