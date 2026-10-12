/**
 * Mobile voice-session surface (RUYI-449). Mounts when `agentId` is
 * non-null: starts a MobileVoiceSession against the agent's bound Voice
 * Runtime through the shared 425 chain, shows connecting/live state and
 * live transcripts, and ends on close. Failures degrade to a dismissible
 * message — text sending is never touched.
 *
 * Both entry points (chat composer mic, new-issue mic) mount this with
 * their own agent target; the new-issue entry additionally backfills each
 * finalized spoken turn into its prompt via `onUserTurn`.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Modal, Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/use-t";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import {
  voiceFailureText,
  type VoiceFailure,
} from "@/lib/voice/failure-text";
import {
  VoiceRejectionError,
} from "@multica/core/voice";
import { MobileVoiceSession } from "@/lib/voice/session";

interface Props {
  /** Non-null mounts the sheet and starts the session. */
  agentId: string | null;
  workspaceSlug: string;
  onClose: () => void;
  /** Finalized spoken turns (new-issue prompt backfill). */
  onUserTurn?: (text: string) => void;
}

type Phase = "connecting" | "live" | "failed" | "ended";

export function VoiceSessionOverlay({
  agentId,
  workspaceSlug,
  onClose,
  onUserTurn,
}: Props) {
  const { t } = useT("voice");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [phase, setPhase] = useState<Phase>("connecting");
  const [failure, setFailure] = useState<VoiceFailure | null>(null);
  const [liveUser, setLiveUser] = useState("");
  const [liveAssistant, setLiveAssistant] = useState("");
  const sessionRef = useRef<MobileVoiceSession | null>(null);
  const onUserTurnRef = useRef(onUserTurn);
  onUserTurnRef.current = onUserTurn;

  useEffect(() => {
    if (!agentId) return;
    setPhase("connecting");
    setFailure(null);
    setLiveUser("");
    setLiveAssistant("");
    const session = new MobileVoiceSession(agentId, workspaceSlug, {
      onStateChange: (state) => {
        if (state === "live") setPhase("live");
      },
      onUserTranscriptDelta: (delta) => setLiveUser((prev) => prev + delta),
      onAssistantTranscriptDelta: (delta) => setLiveAssistant((prev) => prev + delta),
      onUserTurn: (text) => {
        setLiveUser("");
        onUserTurnRef.current?.(text);
      },
      onDegrade: (rejection) => {
        setFailure({ kind: "rejection", rejection });
        setPhase("failed");
      },
      onEnded: () => {
        setPhase((prev) => (prev === "failed" ? prev : "ended"));
      },
    });
    sessionRef.current = session;
    session.start().catch((error: Error) => {
      // 权限被拒给专属引导；带类型化拒绝的失败（服务端 §4.4 闸门 409、
      // provider 不可达）已由 onDegrade 记录精确文案，这里不覆盖；其余
      // 哨兵一律走通用降级文案 —— 文本发送不受影响。
      if (error.message === "VOICE_PERMISSION_DENIED") {
        setFailure({ kind: "permission" });
      } else if (!(error instanceof VoiceRejectionError)) {
        setFailure({ kind: "rejection", rejection: null });
      }
      setPhase("failed");
    });
    return () => {
      session.end();
      sessionRef.current = null;
    };
  }, [agentId, workspaceSlug]);

  const handleEnd = useCallback(() => {
    sessionRef.current?.end();
    sessionRef.current = null;
    onClose();
  }, [onClose]);

  const handleDismissFailure = useCallback(() => {
    sessionRef.current?.end();
    sessionRef.current = null;
    onClose();
  }, [onClose]);

  const visible = agentId != null;
  const failureText = failure
    ? voiceFailureText(failure, (key, dv) => t(key, { defaultValue: dv }))
    : null;

  return (
    <Modal visible={visible} transparent animationType="fade" onRequestClose={handleEnd}>
      <View className="flex-1 items-center justify-center bg-black/50 p-6">
        <View className="w-full max-w-sm rounded-2xl bg-background p-5">
          <View className="items-center gap-3">
            <View
              className="h-14 w-14 items-center justify-center rounded-full"
              style={{
                backgroundColor: phase === "live" ? theme.primary + "22" : theme.muted,
              }}
            >
              <Ionicons
                name="mic"
                size={26}
                color={phase === "live" ? theme.primary : theme.mutedForeground}
              />
            </View>
            <Text className="text-lg font-semibold">{t("overlay.title", "Voice conversation")}</Text>
            {failureText ? (
              <Text className="text-center text-sm" style={{ color: theme.destructive }}>
                {failureText}
              </Text>
            ) : (
              <>
                <View className="flex-row items-center gap-2">
                  {phase === "connecting" && <ActivityIndicator size="small" />}
                  <Text className="text-sm" style={{ color: theme.mutedForeground }}>
                    {phase === "live"
                      ? t("overlay.live", "Listening — speak naturally")
                      : t("overlay.connecting", "Connecting…")}
                  </Text>
                </View>
                {liveUser ? (
                  <Text className="w-full text-sm">
                    {t("overlay.user_label", "You")}: {liveUser}
                  </Text>
                ) : null}
                {liveAssistant ? (
                  <Text className="w-full text-sm" style={{ color: theme.mutedForeground }}>
                    {t("overlay.assistant_label", "Agent")}: {liveAssistant}
                  </Text>
                ) : null}
              </>
            )}
            <View className="mt-2 w-full flex-row justify-center gap-2">
              {failureText ? (
                <Button variant="outline" onPress={handleDismissFailure} className="flex-1">
                  <Text>{t("overlay.dismiss", "Dismiss")}</Text>
                </Button>
              ) : (
                <Pressable
                  onPress={handleEnd}
                  className="flex-1 items-center rounded-md py-2"
                  style={{ backgroundColor: theme.muted }}
                  accessibilityLabel={t("button.stop", "End voice conversation")}
                >
                  <Text>{t("overlay.end", "End")}</Text>
                </Pressable>
              )}
            </View>
          </View>
        </View>
      </View>
    </Modal>
  );
}
