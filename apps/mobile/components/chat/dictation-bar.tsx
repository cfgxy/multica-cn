/**
 * 听写长条（RUYI-694 板1 形态C）：整条替换输入条，高 52、全胶囊、
 * 阴影与主条一致。
 *
 *   - rest：[×][按住 说话 + 幽灵波][幽灵发送钮]
 *   - recording：[×][红点 + 计时 + 波形 + 转写预览][蓝发送钮]；降级
 *     （degraded，RUYI-710）无预览，显「松开后转写」角标
 *   - cancelArmed：整条变红「松开取消」
 *   - finalizing：[×][spinner]（端侧 stop 后 flush，瞬时）
 *   - transcribing：[×][spinner + 转写中…][蓝发送钮]（降级分支：录音
 *     已交后端转写）
 *
 * PTT 手势：按住条体任意处起录（120ms 起录门在 hook）、上滑 >48dp 转
 * 取消态、松开即结算；× 独立可点（子 responder 优先于父手势）。
 */
import { useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  View,
  type StyleProp,
  type ViewStyle,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import Animated, { FadeIn, FadeOut } from "react-native-reanimated";
import { Text } from "@/components/ui/text";
import { type DictationState } from "@/lib/voice/dictation";

import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

const BAR_HEIGHT = 52;

interface Props {
  state: DictationState;
  rms: number[];
  onClose: () => void;
  onPressStart: () => void;
  onPressMove: (upDp: number) => void;
  onPressEnd: () => void;
  /** 发送位的 Stop 形态（in-flight 任务）；默认蓝发送钮。 */
  sending?: boolean;
  onStop?: () => void;
}

export function DictationBar({
  state,
  rms,
  onClose,
  onPressStart,
  onPressMove,
  onPressEnd,
  sending = false,
  onStop,
}: Props) {
  const { t } = useT("voice");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  // PTT：条体触摸面。grant 记起点（dy 基准），move 换算向上 dp，
  // release/terminate 结算。轻点（未满起录门）由 hook 撤门回 rest。
  const startYRef = useRef<number | null>(null);
  const touchHandlers = useMemo(
    () => ({
      onStartShouldSetResponder: () => true,
      onResponderGrant: (e: { nativeEvent: { pageY: number } }) => {
        startYRef.current = e.nativeEvent.pageY;
        onPressStart();
      },
      onResponderMove: (e: { nativeEvent: { pageY: number } }) => {
        const start = startYRef.current;
        if (start === null) return;
        // pageY 减小 = 向上；dy 单位 dp（RN 触摸事件与布局同单位）。
        onPressMove(Math.max(0, start - e.nativeEvent.pageY));
      },
      onResponderRelease: () => {
        startYRef.current = null;
        onPressEnd();
      },
      onResponderTerminate: () => {
        startYRef.current = null;
        onPressEnd();
      },
    }),
    [onPressStart, onPressMove, onPressEnd],
  );

  const recording = state.phase === "recording";
  const cancelArmed = recording && state.cancelArmed;
  const busy = state.phase === "finalizing" || state.phase === "transcribing";

  const barStyle: StyleProp<ViewStyle> = {
    height: BAR_HEIGHT,
    borderRadius: BAR_HEIGHT / 2,
    backgroundColor: cancelArmed ? "#ef4444" : theme.secondary,
    shadowColor: "#0f172a",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.07,
    shadowRadius: 14,
    elevation: 3,
  };

  return (
    <Animated.View
      entering={FadeIn.springify().duration(240)}
      exiting={FadeOut.springify().duration(240)}
      className="flex-row items-center px-2"
      style={barStyle}
    >
      <Pressable
        onPress={onClose}
        hitSlop={10}
        accessibilityRole="button"
        accessibilityLabel={t("dictation.close", "Close dictation")}
        className="h-9 w-9 items-center justify-center rounded-full"
      >
        <Ionicons
          name="close"
          size={22}
          color={cancelArmed ? "#ffffff" : theme.mutedForeground}
        />
      </Pressable>

      <View
        {...touchHandlers}
        className="flex-1 flex-row items-center"
        style={{ minHeight: BAR_HEIGHT }}
      >
        {cancelArmed ? (
          <Text className="flex-1 text-center text-sm font-medium text-white">
            {t("dictation.release_to_cancel", "Release to cancel")}
          </Text>
        ) : recording ? (
          <RecordingBody state={state} rms={rms} />
        ) : busy ? (
          <View className="flex-1 flex-row items-center justify-center gap-2">
            <ActivityIndicator size="small" color={theme.mutedForeground} />
            {state.phase === "transcribing" ? (
              <Text className="text-sm text-muted-foreground">
                {t("dictation.transcribing", "Transcribing…")}
              </Text>
            ) : null}
          </View>
        ) : (
          <RestBody />
        )}
      </View>

      {cancelArmed ? (
        <View className="h-9 w-9" />
      ) : sending && onStop ? (
        <Pressable
          onPress={onStop}
          hitSlop={10}
          accessibilityRole="button"
          accessibilityLabel={t("dictation.stop_agent", "Stop agent")}
          className="h-9 w-9 items-center justify-center rounded-full bg-foreground"
        >
          <View style={{ width: 10, height: 10, backgroundColor: theme.background, borderRadius: 1.5 }} />
        </Pressable>
      ) : (
        <View className="h-9 w-9 items-center justify-center rounded-full bg-primary">
          <Ionicons name="arrow-up" size={20} color={theme.primaryForeground} />
        </View>
      )}
    </Animated.View>
  );
}

function RestBody() {
  const { t } = useT("voice");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return (
    <View className="flex-1 flex-row items-center gap-2 px-2">
      <Text className="text-sm text-muted-foreground">
        {t("dictation.hold_to_talk", "Hold to talk")}
      </Text>
      <GhostWaves />
    </View>
  );
}

/** rest 态的幽灵波纹：三根静态矮条，视觉占位。 */
function GhostWaves() {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return (
    <View className="flex-row items-center gap-1">
      {[8, 12, 8].map((h, i) => (
        <View
          key={i}
          style={{
            width: 3,
            height: h,
            borderRadius: 2,
            backgroundColor: theme.border,
          }}
        />
      ))}
    </View>
  );
}

function RecordingBody({
  state,
  rms,
}: {
  state: DictationState;
  rms: number[];
}) {
  const { t } = useT("voice");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [nowMs, setNowMs] = useState(() => Date.now());

  useEffect(() => {
    if (state.startedAtMs === null) return;
    const timer = setInterval(() => setNowMs(Date.now()), 500);
    return () => clearInterval(timer);
  }, [state.startedAtMs]);

  const elapsed = state.startedAtMs === null ? 0 : Math.max(0, nowMs - state.startedAtMs);
  const mm = Math.floor(elapsed / 60_000);
  const ss = Math.floor((elapsed % 60_000) / 1000);
  const clock = `${String(mm).padStart(2, "0")}:${String(ss).padStart(2, "0")}`;

  return (
    <View className="flex-1 flex-row items-center gap-2 px-2">
      <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: "#ef4444" }} />
      <Text className="text-xs font-medium tabular-nums text-muted-foreground">
        {clock}
      </Text>
      <Waveform rms={rms} />
      {state.degraded ? (
        // 降级录制（RUYI-710 板 1 五态）：端侧无识别，无预览，松开后
        // 交后端转写——角标预告出口。
        <Text className="flex-1 text-sm text-muted-foreground" numberOfLines={1}>
          {t("dictation.release_to_transcribe", "Release to transcribe")}
        </Text>
      ) : state.preview ? (
        <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
          {state.preview}
        </Text>
      ) : (
        <Text className="flex-1 text-sm text-muted-foreground" numberOfLines={1}>
          {t("dictation.listening", "Listening…")}
        </Text>
      )}
    </View>
  );
}

/** 波形：rms 采样映射条高（4..20dp），槽位不足左侧补底。 */
function Waveform({ rms }: { rms: number[] }) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const slots = 14;
  const sampled = rms.slice(-slots);
  return (
    <View className="flex-row items-center gap-[3px]">
      {Array.from({ length: slots }, (_, i) => {
        const v = sampled[i];
        const height = v === undefined ? 4 : 4 + v * 16;
        return (
          <View
            key={i}
            style={{
              width: 3,
              height,
              borderRadius: 2,
              backgroundColor:
                v === undefined ? theme.border : theme.primary,
            }}
          />
        );
      })}
    </View>
  );
}

