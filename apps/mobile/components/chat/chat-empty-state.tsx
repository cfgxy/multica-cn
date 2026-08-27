/**
 * Empty-state surface shown when the active session has no messages.
 *
 * Mirrors web (packages/views/chat/components/chat-window.tsx `EmptyState`):
 * the title is the agent greeting, the agent's description sits under it,
 * and the starter buttons come from the agent's configured
 * conversation starters — falling back to the same generic three the web
 * shows (via `selectConversationStarters`). Tap prefills the composer draft
 * so the user can edit before sending.
 *
 * Copy mirrors the web `chat.json` namespace 1:1 — the lookup keys
 * (`empty_state.*`, `conversation_starters.*`) were already established on
 * the web side, so this screen's i18n adoption was a literal key-by-key swap.
 */
import { View } from "react-native";
import { selectConversationStarters } from "@multica/core/agents";
import type { Agent, AgentConversationStarter } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/use-t";

interface Props {
  hasSessions: boolean;
  agent: Agent | null;
  onPickPrompt: (text: string) => void;
}

export function ChatEmptyState({ hasSessions, agent, onPickPrompt }: Props) {
  const { t } = useT("chat");

  const title = agent
    ? t("empty_state.chat_with_named", "Chat with {{name}}", {
        name: agent.name,
      })
    : t("empty_state.first_time_title", "Chat with your agents");
  const description = agent?.description?.trim();

  // agent 未配置开场建议时的三条兜底提示。与 web 的
  // useFallbackConversationStarters() 同一组 key —— 常量在模块作用域取不到
  // Hook，翻译必须在组件内做（web 侧同理，用 hook 包了一层）。
  const fallbackStarters: AgentConversationStarter[] = [
    {
      label: t(
        "conversation_starters.capabilities.label",
        "What can you help with?",
      ),
      prompt: t(
        "conversation_starters.capabilities.prompt",
        "What are you best at helping with? Give me a concise overview.",
      ),
    },
    {
      label: t(
        "conversation_starters.first_task.label",
        "Suggest a first task",
      ),
      prompt: t(
        "conversation_starters.first_task.prompt",
        "Suggest three useful tasks I could delegate to you.",
      ),
    },
    {
      label: t("conversation_starters.recommend.label", "Recommend an action"),
      prompt: t(
        "conversation_starters.recommend.prompt",
        "Review what you know about my workspace and recommend a useful first action.",
      ),
    },
  ];
  const { starters } = selectConversationStarters(
    agent?.conversation_starters,
    fallbackStarters,
  );

  return (
    <View className="flex-1 items-center justify-center px-6 py-8 gap-5">
      <View className="items-center gap-1">
        <Text className="text-base font-semibold text-foreground text-center">
          {title}
        </Text>
        {description ? (
          <Text className="text-sm text-muted-foreground text-center">
            {description}
          </Text>
        ) : null}
        {!hasSessions ? (
          <Text className="text-sm text-muted-foreground text-center">
            {t(
              "empty_state.first_time_actions",
              "Ask for a summary, plan your day, or hand off a quick task.",
            )}
          </Text>
        ) : null}
      </View>
      {agent ? (
        <View
          className="w-full max-w-xs gap-2"
          accessibilityLabel={t(
            "conversation_starters.aria_label",
            "Conversation starters",
          )}
        >
          {starters.map((item, index) => (
            <Button
              key={index}
              variant="outline"
              onPress={() => onPickPrompt(item.prompt)}
              className="h-auto justify-start px-3 py-2.5"
              accessibilityLabel={item.label}
            >
              <Text className="text-sm text-foreground">{item.label}</Text>
            </Button>
          ))}
        </View>
      ) : null}
    </View>
  );
}
