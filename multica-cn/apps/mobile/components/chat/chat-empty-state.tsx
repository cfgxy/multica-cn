/**
 * Empty-state surface shown when the active session has no messages.
 *
 * Mirrors web (packages/views/chat/components/chat-empty-state.tsx):
 * agent-configured conversation starters (`agent.conversation_starters`)
 * win when present; the built-in starters are the fallback. Configured
 * starters and the agent description are user content — they render
 * verbatim by design (no translation). All built-in UI copy goes through
 * t() on the shared web `chat` namespace, so every locale resource that
 * web ships resolves here too.
 */
import { View } from "react-native";
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

  // Upstream `selectConversationStarters` semantics: configured starters
  // win only when both label and prompt are non-blank. User content —
  // rendered verbatim, never translated.
  const configured = (agent?.conversation_starters ?? []).filter(
    (item) => item.label.trim() && item.prompt.trim(),
  );

  // Built-in fallback starters reuse the web `conversation_starters.*`
  // keys (the old `starter_prompts.*` keys were removed upstream when
  // starters became agent-configurable), so no mobile-only duplicate
  // namespace is created. Fallback text must match the en resource 1:1 —
  // the i18n-keys test enforces it.
  const fallbackStarters = [
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
      label: t("conversation_starters.first_task.label", "Suggest a first task"),
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
  const starters = configured.length > 0 ? configured : fallbackStarters;

  const title = agent
    ? t("empty_state.returning_title_named", "Hi, I'm {{name}}", {
        name: agent.name,
      })
    : t("empty_state.first_time_title", "Chat with your agents");

  return (
    <View className="flex-1 items-center justify-center px-6 py-8 gap-5">
      <View className="items-center gap-1">
        <Text className="text-base font-semibold text-foreground text-center">
          {title}
        </Text>
        {agent?.description ? (
          <Text className="text-sm text-muted-foreground text-center">
            {agent.description}
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
        <View className="w-full max-w-xs gap-2">
          {starters.map((item, index) => {
            // 译文既是按钮文案，也是点击后填进输入框的草稿内容 —— 用户看到
            // 中文提示、点了却填进英文原文会很突兀，两处必须同一个值。
            return (
              <Button
                key={index}
                variant="outline"
                onPress={() => onPickPrompt(item.prompt)}
                className="h-auto justify-start px-3 py-2.5"
                accessibilityLabel={item.label}
              >
                <Text className="text-sm text-foreground">{item.label}</Text>
              </Button>
            );
          })}
        </View>
      ) : null}
    </View>
  );
}
