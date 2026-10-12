/**
 * 语音球（RUYI-694 板3）：六态渲染 + 板3 动效参数。
 *
 *   - 呼吸：scale 1.0↔1.06 / 1600ms ease-in-out 循环（UserSpeaking
 *     幅度升到 1.1 / 900ms）。
 *   - 涟漪：Speaking 双环 r×1.12/1.26，2400ms 外扩淡出循环。
 *   - 思考弧：两段 90° 弧旋转，1200ms linear，紫色。
 *   - 打断：scaleY 0.955，600ms；glow 变红。
 *
 * 全部动效仅 transform/opacity；系统「减弱动效」开启时退化为静态
 * 色板（板6 可访问性）。球面用底色圆 + 左上高光近似径向渐变，
 * 零原生依赖。
 */
import { useEffect } from "react";
import { AccessibilityInfo, Pressable, View } from "react-native";
import Animated, {
  cancelAnimation,
  Easing,
  useAnimatedStyle,
  useSharedValue,
  withRepeat,
  withTiming,
} from "react-native-reanimated";
import { orbPalette, type OrbPhase } from "@/lib/voice/orb-state";

interface Props {
  phase: OrbPhase;
  /** 直径 dp。 */
  size: number;
  /** 点按（模式互切）；connecting 与异常态不响应。 */
  onPress?: () => void;
  accessibilityLabel?: string;
}

export function VoiceOrb({ phase, size, onPress, accessibilityLabel }: Props) {
  const palette = orbPalette(phase);
  const scale = useSharedValue(1);
  const scaleY = useSharedValue(1);
  const arcRotation = useSharedValue(0);
  const ripple1 = useSharedValue(0);
  const ripple2 = useSharedValue(0);
  const rippleOpacity = useSharedValue(0);
  const reduceMotion = useSharedValue(false);

  useEffect(() => {
    AccessibilityInfo.isReduceMotionEnabled().then((enabled) => {
      reduceMotion.value = enabled;
    });
    const sub = AccessibilityInfo.addEventListener(
      "reduceMotionChanged",
      (enabled) => {
        reduceMotion.value = enabled;
      },
    );
    return () => sub.remove();
  }, []);

  useEffect(() => {
    cancelAnimation(scale);
    cancelAnimation(arcRotation);
    cancelAnimation(ripple1);
    cancelAnimation(ripple2);
    cancelAnimation(rippleOpacity);
    scaleY.value = withTiming(1, { duration: 200 });

    if (reduceMotion.value) {
      scale.value = 1;
      return;
    }

    switch (phase) {
      case "connecting":
      case "listening":
      case "speaking":
      case "disconnected":
      case "failed":
        scale.value = withRepeat(
          withTiming(1.06, {
            duration: 1600,
            easing: Easing.inOut(Easing.ease),
          }),
          -1,
          true,
        );
        break;
      case "userSpeaking":
        // 波动增强：幅度随音量（板3 1.0~1.1）；端侧无声级回调，取
        // 上限幅度 + 更快周期近似。
        scale.value = withRepeat(
          withTiming(1.1, { duration: 900, easing: Easing.inOut(Easing.ease) }),
          -1,
          true,
        );
        break;
      case "thinking":
        scale.value = 1;
        arcRotation.value = withRepeat(
          withTiming(360, { duration: 1200, easing: Easing.linear }),
          -1,
          false,
        );
        break;
      case "interrupted":
        scale.value = 1;
        scaleY.value = withTiming(0.955, { duration: 600 });
        break;
    }

    if (phase === "speaking") {
      ripple1.value = 1;
      ripple2.value = 1;
      rippleOpacity.value = withRepeat(
        withTiming(0, { duration: 2400, easing: Easing.out(Easing.ease) }),
        -1,
        false,
      );
      ripple1.value = withRepeat(
        withTiming(1.12, { duration: 2400, easing: Easing.out(Easing.ease) }),
        -1,
        false,
      );
      ripple2.value = withRepeat(
        withTiming(1.26, { duration: 2400, easing: Easing.out(Easing.ease) }),
        -1,
        false,
      );
    } else {
      rippleOpacity.value = 0;
    }
    // reduceMotion.value 不入 deps：启用/关闭由 reduceMotionChanged
    // 事件驱动，下一次 phase effect 或重渲染自然收敛。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phase]);

  const orbStyle = useAnimatedStyle(() => ({
    transform: [{ scale: scale.value }, { scaleY: scaleY.value }],
  }));
  const arcStyle = useAnimatedStyle(() => ({
    transform: [{ rotate: `${arcRotation.value}deg` }],
  }));
  const ripple1Style = useAnimatedStyle(() => ({
    transform: [{ scale: ripple1.value }],
    opacity: rippleOpacity.value,
  }));
  const ripple2Style = useAnimatedStyle(() => ({
    transform: [{ scale: ripple2.value }],
    opacity: rippleOpacity.value,
  }));

  const tapEnabled = onPress && phase !== "connecting" && phase !== "failed" && phase !== "disconnected";
  const showArc = phase === "thinking";
  const highlight = Math.round(size * 0.34);

  return (
    <Pressable
      onPress={tapEnabled ? onPress : undefined}
      disabled={!tapEnabled}
      accessibilityRole={onPress ? "button" : undefined}
      accessibilityLabel={accessibilityLabel}
      style={{ width: size, height: size, alignItems: "center", justifyContent: "center" }}
    >
      {phase === "speaking" ? (
        <>
          <Animated.View
            pointerEvents="none"
            style={[
              {
                position: "absolute",
                width: size,
                height: size,
                borderRadius: size / 2,
                borderWidth: 2,
                borderColor: palette.glow,
              },
              ripple1Style,
            ]}
          />
          <Animated.View
            pointerEvents="none"
            style={[
              {
                position: "absolute",
                width: size,
                height: size,
                borderRadius: size / 2,
                borderWidth: 2,
                borderColor: palette.glow,
              },
              ripple2Style,
            ]}
          />
        </>
      ) : null}
      <Animated.View style={[{ width: size, height: size }, orbStyle]}>
        <View
          style={{
            width: size,
            height: size,
            borderRadius: size / 2,
            backgroundColor: palette.core,
            alignItems: "center",
            justifyContent: "center",
          }}
        >
          {showArc ? (
            <Animated.View
              pointerEvents="none"
              style={[
                {
                  position: "absolute",
                  width: size + 14,
                  height: size + 14,
                  borderRadius: (size + 14) / 2,
                  borderWidth: 3,
                  borderTopColor: palette.glow,
                  borderRightColor: "transparent",
                  borderBottomColor: "transparent",
                  borderLeftColor: "transparent",
                },
                arcStyle,
              ]}
            />
          ) : null}
          {/* 左上高光：径向渐变的零依赖近似。 */}
          <View
            style={{
              position: "absolute",
              top: size * 0.12,
              left: size * 0.18,
              width: highlight,
              height: highlight * 0.72,
              borderRadius: highlight / 2,
              backgroundColor: "rgba(255,255,255,0.28)",
            }}
          />
        </View>
      </Animated.View>
    </Pressable>
  );
}
