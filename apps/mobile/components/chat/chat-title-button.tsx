/**
 * Centred title region for the chat screen header.
 *
 * With `onPress` (legacy tab-screen usage): the whole region is a tappable
 * Pressable rendered as `headerTitle: () => ...`, opening the sessions
 * sheet. Without it (RUYI-496 detail screen): display-only identity — the
 * list is the session switcher now, so no ▼ affordance and no button
 * semantics.
 */
import { Pressable, View } from "react-native";
import type { Agent, ChatSession } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { useT } from "@/lib/use-t";
import { chatSessionDisplayTitle } from "@/lib/chat-session-title";

interface Props {
  currentSession: ChatSession | null;
  currentAgent: Agent | null;
  onPress?: () => void;
}

export function ChatTitleButton({
  currentSession,
  currentAgent,
  onPress,
}: Props) {
  const { t } = useT("chat");
  const agentName = currentAgent?.name ?? "Chat";
  const subtitle = chatSessionDisplayTitle(
    currentSession?.title,
    t("mobile.sessions.untitled", "Untitled chat"),
  );

  const identity = (
    <>
      <ActorAvatar
        type={currentAgent ? "agent" : null}
        id={currentAgent?.id ?? null}
        size={24}
        showPresence
      />
      <View>
        <View className="flex-row items-center gap-1">
          <Text
            className="text-base font-semibold text-foreground"
            numberOfLines={1}
          >
            {agentName}
          </Text>
          {onPress ? (
            <Text className="text-xs text-muted-foreground">▼</Text>
          ) : null}
        </View>
        <Text
          className="text-xs text-muted-foreground"
          numberOfLines={1}
        >
          {subtitle}
        </Text>
      </View>
    </>
  );

  if (!onPress) {
    return (
      <View className="flex-row items-center gap-2 px-2 py-1">{identity}</View>
    );
  }

  return (
    <Pressable
      onPress={onPress}
      hitSlop={4}
      className="flex-row items-center gap-2 px-2 py-1 rounded-lg active:bg-secondary"
      accessibilityRole="button"
      accessibilityLabel={t(
        "mobile.sessions.title_button_a11y",
        "Sessions and agent picker",
      )}
    >
      {identity}
    </Pressable>
  );
}
