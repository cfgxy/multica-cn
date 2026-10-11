/**
 * Mobile voice-session surface — RUYI-694 板3/板4/板6 三形态重写。
 *
 *   - full（模式一·沉浸）：Modal 全屏固定深色 #0B0F1A，左上 × 挂断、
 *     右上静音、居中语音球 160dp（点按切模式二）、球下 15.5dp 状态文案。
 *   - inline（模式二·同屏）：absolute 覆盖聊天页，顶条（收起 / 标题 +
 *     实时状态 / 挂断）+ 84dp 球（点按回沉浸）+ 双向转写时间线 + 控制
 *     行（静音 / 挂断红 54 / 沉浸）。文字注入框降级不做：网关协议无
 *     文本注入帧（core VoiceSessionController 无 sendText），需要打字
 *     时收起为迷你球（交付回报注明）。
 *   - mini：58dp 悬浮球，拖动吸左/右壁（边距 16，距底 118），点按展开
 *     回模式二；相位归并经 `miniPhase`。
 *
 * 会话生命周期（建立/挂断/静音/重连/自动挂断护栏）在
 * `useVoiceSessionRuntime`；失败态沿用 RUYI-449 的降级卡片文案
 * （`voiceFailureText`）。挂断任意形态统一收尾：回聊天页 + toast。
 */
import { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Modal,
  PanResponder,
  Platform,
  Pressable,
  ScrollView,
  Text as RNText,
  ToastAndroid,
  View,
  useWindowDimensions,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import Animated, { FadeIn, FadeOut } from "react-native-reanimated";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/use-t";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import {
  miniPhase,
  orbPalette,
  orbStatusLabelKey,
  type MiniPhase,
  type OrbPhase,
} from "@/lib/voice/orb-state";
import {
  useVoiceSessionRuntime,
  type AutoEndReason,
  type TranscriptEntry,
} from "@/lib/voice/use-voice-session";
import { voiceFailureText } from "@/lib/voice/failure-text";
import { VoiceOrb } from "./voice-orb";

type SurfaceMode = "full" | "inline" | "mini";

interface Props {
  /** Non-null mounts the surface and starts the session. */
  agentId: string | null;
  workspaceSlug: string;
  onClose: () => void;
  /** Finalized spoken turns（聊天页回填 draft；new-issue 入口回填 prompt）。 */
  onUserTurn?: (text: string) => void;
}

const MINI_SIZE = 58;
const MINI_MARGIN = 16;
const MINI_BOTTOM = 118;
/** 沉浸/同屏固定深色底（板3 设计 token，不随全局主题）。 */
const IMMERSIVE_BG = "#0B0F1A";

/** 状态文案 fallback（en 资源同文；动态 key 调用不进 i18n 静态校验）。 */
const STATUS_FALLBACKS: Record<OrbPhase, string> = {
  connecting: "Connecting…",
  listening: "Listening",
  userSpeaking: "Listening to you",
  thinking: "Thinking",
  speaking: "Responding",
  interrupted: "Interrupted",
  disconnected: "Disconnected",
  failed: "Voice isn't available",
};

export function VoiceSessionOverlay({
  agentId,
  workspaceSlug,
  onClose,
  onUserTurn,
}: Props) {
  const { t } = useT("voice");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [mode, setMode] = useState<SurfaceMode>("full");

  // 会话收尾（手动/自动挂断统一走 hook 的 onEnded）：toast + 关面板。
  const handleEnded = useCallback(
    (reason: AutoEndReason | null) => {
      const message =
        reason === "idle_timeout"
          ? t(
              "orb.toast_saved_idle",
              "Voice conversation saved (idle timeout)",
            )
          : reason === "background_timeout"
            ? t(
                "orb.toast_saved_background",
                "Voice conversation saved (backgrounded too long)",
              )
            : t("orb.toast_saved", "Voice conversation saved");
      if (Platform.OS === "android") {
        ToastAndroid.show(message, ToastAndroid.SHORT);
      } else {
        Alert.alert(message);
      }
      onClose();
    },
    [t, onClose],
  );

  const runtime = useVoiceSessionRuntime({
    agentId,
    workspaceSlug,
    onUserTurn,
    onEnded: handleEnded,
  });

  // 面板重开时回沉浸态（聊天页常挂载，agentId 切换不 remount）。
  useEffect(() => {
    if (agentId) setMode("full");
  }, [agentId]);

  const hangUp = runtime.hangUp;
  const statusLabel = t(
    orbStatusLabelKey(runtime.orb),
    STATUS_FALLBACKS[runtime.orb],
  );
  const palette = orbPalette(runtime.orb);
  const failureText = runtime.failure
    ? voiceFailureText(runtime.failure, (key, dv) =>
        t(key, { defaultValue: dv }),
      )
    : null;

  const labels = {
    hangUp: t("orb.hangup", "Hang up"),
    mute: t("orb.mute", "Mute microphone"),
    unmute: t("orb.unmute", "Unmute microphone"),
    immerse: t("orb.immerse", "Immersive view"),
    collapse: t("orb.collapse", "Collapse to mini orb"),
    reconnect: t("orb.reconnect", "Reconnect"),
    title: t("overlay.title", "Voice conversation"),
  };

  if (agentId == null) return null;

  // 失败终态：任何形态下都弹降级卡片（沿用 RUYI-449 卡片与文案）。
  if (runtime.orb === "failed") {
    return (
      <Modal visible transparent animationType="fade" onRequestClose={hangUp}>
        <View className="flex-1 items-center justify-center bg-black/50 p-6">
          <View className="w-full max-w-sm rounded-2xl bg-background p-5">
            <View className="items-center gap-3">
              <Text className="text-lg font-semibold">{labels.title}</Text>
              <Text
                className="text-center text-sm"
                style={{ color: theme.destructive }}
              >
                {failureText ??
                  t(
                    "failure.connection_failed",
                    "Voice couldn't connect right now. Text messaging still works as usual.",
                  )}
              </Text>
              <View className="mt-2 w-full">
                <Button variant="outline" onPress={hangUp} className="w-full">
                  <Text>{t("overlay.dismiss", "Dismiss")}</Text>
                </Button>
              </View>
            </View>
          </View>
        </View>
      </Modal>
    );
  }

  const disconnectedActions =
    runtime.orb === "disconnected" ? (
      <View style={{ flexDirection: "row", gap: 12, marginTop: 16 }}>
        <Pressable
          onPress={runtime.reconnect}
          accessibilityRole="button"
          accessibilityLabel={labels.reconnect}
          style={{
            paddingHorizontal: 20,
            paddingVertical: 10,
            borderRadius: 999,
            backgroundColor: "#3B82F6",
          }}
        >
          <RNText style={{ color: "#FFFFFF", fontSize: 14, fontWeight: "600" }}>
            {labels.reconnect}
          </RNText>
        </Pressable>
        <Pressable
          onPress={hangUp}
          accessibilityRole="button"
          accessibilityLabel={labels.hangUp}
          style={{
            paddingHorizontal: 20,
            paddingVertical: 10,
            borderRadius: 999,
            backgroundColor: "rgba(255,255,255,0.12)",
          }}
        >
          <RNText style={{ color: "#FFFFFF", fontSize: 14 }}>
            {labels.hangUp}
          </RNText>
        </Pressable>
      </View>
    ) : null;

  return (
    <>
      {mode !== "mini" ? (
        <Modal
          visible={mode === "full"}
          transparent
          animationType="fade"
          onRequestClose={hangUp}
        >
          <View style={{ flex: 1, backgroundColor: IMMERSIVE_BG }}>
            <View
              style={{
                flexDirection: "row",
                justifyContent: "space-between",
                paddingHorizontal: 8,
                paddingTop: 48,
              }}
            >
              <IconButton icon="close" onPress={hangUp} label={labels.hangUp} />
              <IconButton
                icon={runtime.muted ? "mic-off" : "mic"}
                onPress={() => runtime.setMuted(!runtime.muted)}
                label={runtime.muted ? labels.unmute : labels.mute}
              />
            </View>
            <View
              style={{
                flex: 1,
                alignItems: "center",
                justifyContent: "center",
                paddingBottom: 40,
              }}
            >
              <VoiceOrb
                phase={runtime.orb}
                size={160}
                onPress={() => setMode("inline")}
                accessibilityLabel={statusLabel}
              />
              <RNText
                style={{
                  marginTop: 18,
                  fontSize: 15.5,
                  color: palette.label,
                }}
                maxFontSizeMultiplier={1.3}
              >
                {statusLabel}
              </RNText>
              {disconnectedActions}
            </View>
          </View>
        </Modal>
      ) : null}

      {mode === "inline" ? (
        <Animated.View
          entering={FadeIn.duration(220)}
          exiting={FadeOut.duration(220)}
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            right: 0,
            bottom: 0,
            backgroundColor: "#0B0F1AF2",
            zIndex: 50,
          }}
        >
          <View style={{ flex: 1 }}>
            {/* 顶条：收起 / 标题+实时状态 / 挂断 */}
            <View
              style={{
                flexDirection: "row",
                alignItems: "center",
                justifyContent: "space-between",
                paddingHorizontal: 8,
                paddingTop: 48,
                paddingBottom: 4,
              }}
            >
              <IconButton
                icon="chevron-down"
                onPress={() => setMode("mini")}
                label={labels.collapse}
              />
              <View style={{ alignItems: "center" }}>
                <RNText
                  style={{ color: "#FFFFFF", fontSize: 16, fontWeight: "600" }}
                  maxFontSizeMultiplier={1.3}
                >
                  {labels.title}
                </RNText>
                <RNText
                  style={{ color: "rgba(255,255,255,0.55)", fontSize: 12 }}
                  maxFontSizeMultiplier={1.3}
                >
                  {statusLabel}
                </RNText>
              </View>
              <IconButton icon="close" onPress={hangUp} label={labels.hangUp} />
            </View>

            {runtime.orb === "disconnected" ? (
              <View
                style={{
                  flexDirection: "row",
                  alignItems: "center",
                  gap: 10,
                  marginHorizontal: 12,
                  marginBottom: 4,
                  paddingHorizontal: 12,
                  paddingVertical: 8,
                  borderRadius: 12,
                  backgroundColor: "rgba(107,114,128,0.18)",
                }}
              >
                <Ionicons
                  name="cloud-offline-outline"
                  size={16}
                  color="rgba(255,255,255,0.72)"
                />
                <RNText
                  style={{
                    flex: 1,
                    color: "rgba(255,255,255,0.72)",
                    fontSize: 12.5,
                  }}
                  maxFontSizeMultiplier={1.3}
                >
                  {t(
                    "orb.disconnect_hint",
                    "Connection lost — reconnect or hang up.",
                  )}
                </RNText>
                <Pressable
                  onPress={runtime.reconnect}
                  accessibilityRole="button"
                  accessibilityLabel={labels.reconnect}
                  hitSlop={8}
                >
                  <RNText
                    style={{
                      color: "#60A5FA",
                      fontSize: 13,
                      fontWeight: "600",
                    }}
                  >
                    {labels.reconnect}
                  </RNText>
                </Pressable>
              </View>
            ) : null}

            {/* 球体行：点按回沉浸态 */}
            <View style={{ alignItems: "center", paddingVertical: 10 }}>
              <VoiceOrb
                phase={runtime.orb}
                size={84}
                onPress={() => setMode("full")}
                accessibilityLabel={statusLabel}
              />
            </View>

            <TranscriptTimeline entries={runtime.entries} />

            {/* 控制行：静音 / 挂断（红 54 圆钮）/ 沉浸 */}
            <View
              style={{
                flexDirection: "row",
                alignItems: "center",
                justifyContent: "center",
                gap: 28,
                paddingTop: 10,
                paddingBottom: 28,
              }}
            >
              <IconButton
                icon={runtime.muted ? "mic-off" : "mic"}
                onPress={() => runtime.setMuted(!runtime.muted)}
                label={runtime.muted ? labels.unmute : labels.mute}
              />
              <Pressable
                onPress={hangUp}
                accessibilityRole="button"
                accessibilityLabel={labels.hangUp}
                style={{
                  width: 54,
                  height: 54,
                  borderRadius: 27,
                  backgroundColor: "rgba(239,68,68,0.54)",
                  alignItems: "center",
                  justifyContent: "center",
                }}
              >
                <Ionicons
                  name="call"
                  size={24}
                  color="#FFFFFF"
                  style={{ transform: [{ rotate: "135deg" }] }}
                />
              </Pressable>
              <IconButton
                icon="expand"
                onPress={() => setMode("full")}
                label={labels.immerse}
              />
            </View>
          </View>
        </Animated.View>
      ) : null}

      {mode === "mini" ? (
        <MiniOrb
          orb={miniPhase(runtime.orb)}
          statusLabel={statusLabel}
          onExpand={() => setMode("inline")}
        />
      ) : null}
    </>
  );
}

function IconButton({
  icon,
  onPress,
  label,
  color = "rgba(255,255,255,0.78)",
}: {
  icon: keyof typeof Ionicons.glyphMap;
  onPress: () => void;
  label: string;
  color?: string;
}) {
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={label}
      hitSlop={8}
      style={{
        width: 40,
        height: 40,
        borderRadius: 20,
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <Ionicons name={icon} size={22} color={color} />
    </Pressable>
  );
}

/** 板4 双向转写时间线：用户右对齐品牌蓝 10% 底、助手左对齐中性白底。 */
function TranscriptTimeline({ entries }: { entries: TranscriptEntry[] }) {
  const scrollRef = useRef<ScrollView | null>(null);
  const stickToBottom = useCallback(() => {
    scrollRef.current?.scrollToEnd({ animated: false });
  }, []);

  return (
    <ScrollView
      ref={scrollRef}
      onContentSizeChange={stickToBottom}
      onLayout={stickToBottom}
      showsVerticalScrollIndicator={false}
      style={{ flex: 1 }}
      contentContainerStyle={{ paddingHorizontal: 16, paddingVertical: 8 }}
    >
      {entries.map((entry) => (
        <Animated.View
          key={entry.id}
          entering={FadeIn.duration(150)}
          style={{
            flexDirection: "row",
            justifyContent: entry.role === "user" ? "flex-end" : "flex-start",
            marginBottom: 8,
          }}
        >
          <View
            style={{
              maxWidth: "82%",
              backgroundColor:
                entry.role === "user" ? "#3B82F61A" : "rgba(255,255,255,0.08)",
              borderRadius: 14,
              paddingHorizontal: 12,
              paddingVertical: 8,
            }}
          >
            <RNText
              style={{ color: "#FFFFFFE6", fontSize: 14.5, lineHeight: 20 }}
              maxFontSizeMultiplier={1.3}
            >
              {entry.text}
              {entry.streaming ? " ▍" : ""}
            </RNText>
          </View>
        </Animated.View>
      ))}
    </ScrollView>
  );
}

/**
 * 板4 迷你球：PanResponder 拖动 + 松手吸左/右壁；位移 <10dp 判点按
 * 展开模式二。相位经 `miniPhase` 归并（VoiceOrb 只接 OrbPhase，子集
 * 类型直接兼容）。
 */
function MiniOrb({
  orb,
  statusLabel,
  onExpand,
}: {
  orb: MiniPhase;
  statusLabel: string;
  onExpand: () => void;
}) {
  const { width, height } = useWindowDimensions();
  const [pos, setPos] = useState(() => ({
    x: width - MINI_SIZE - MINI_MARGIN,
    y: height - MINI_BOTTOM - MINI_SIZE,
  }));
  const dragRef = useRef<{
    startX: number;
    startY: number;
    originX: number;
    originY: number;
    moved: boolean;
  } | null>(null);
  const onExpandRef = useRef(onExpand);
  onExpandRef.current = onExpand;

  const clamp = useCallback(
    (x: number, y: number) => ({
      x: Math.min(Math.max(0, x), Math.max(0, width - MINI_SIZE)),
      y: Math.min(Math.max(0, y), Math.max(0, height - MINI_SIZE)),
    }),
    [width, height],
  );

  const pan = PanResponder.create({
    onStartShouldSetPanResponder: () => true,
    onMoveShouldSetPanResponder: () => true,
    onPanResponderGrant: (e) => {
      dragRef.current = {
        startX: e.nativeEvent.pageX,
        startY: e.nativeEvent.pageY,
        originX: pos.x,
        originY: pos.y,
        moved: false,
      };
    },
    onPanResponderMove: (e, g) => {
      const drag = dragRef.current;
      if (!drag) return;
      if (Math.abs(g.dx) > 10 || Math.abs(g.dy) > 10) drag.moved = true;
      if (drag.moved) setPos(clamp(drag.originX + g.dx, drag.originY + g.dy));
    },
    onPanResponderRelease: (e, g) => {
      const drag = dragRef.current;
      dragRef.current = null;
      if (!drag) return;
      if (!drag.moved) {
        onExpandRef.current();
        return;
      }
      // 吸附：球心在左半屏吸左壁，否则吸右壁（边距 16）。
      const centerY = drag.originY + g.dy + MINI_SIZE / 2;
      const nextX =
        drag.originX + g.dx + MINI_SIZE / 2 < width / 2
          ? MINI_MARGIN
          : width - MINI_SIZE - MINI_MARGIN;
      setPos(clamp(nextX, centerY - MINI_SIZE / 2));
    },
  });

  return (
    <View
      {...pan.panHandlers}
      accessibilityRole="button"
      accessibilityLabel={statusLabel}
      style={{
        position: "absolute",
        left: pos.x,
        top: pos.y,
        width: MINI_SIZE,
        height: MINI_SIZE,
        zIndex: 60,
      }}
    >
      <VoiceOrb phase={orb} size={MINI_SIZE} />
    </View>
  );
}
