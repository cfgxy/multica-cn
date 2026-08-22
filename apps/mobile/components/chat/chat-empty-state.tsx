/**
 * Empty-state surface shown when the active session has no messages.
 *
 * Two modes mirror web (packages/views/chat/components/chat-window.tsx
 * `EmptyState`):
 *
 *   - first-time (the workspace has never started a chat) → educate. Tell
 *     the user what chat is for; don't surface starter prompts yet, they
 *     presume context the user doesn't have.
 *   - returning (at least one prior session exists) → starter prompts.
 *     Three taps, three common workflows; tapping prefills the composer
 *     draft so the user can edit before sending.
 *
 * Copy mirrors the web `chat.json` namespace 1:1 — the lookup keys
 * (`empty_state.first_time_title` etc.) were already established on the web
 * side, so this screen's i18n adoption was a literal key-by-key swap.
 */
import { View } from "react-native";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/use-t";

/**
 * 三条起始提示的 key 与图标。文案本身走 t()，所以数组里只留 key ——
 * 常量在模块作用域，取不到 Hook，翻译必须在组件内做。
 *
 * 英文原文从 mobile 自己的措辞（"...open issues..." / "Help me plan..."）
 * 换成 web `chat.json` 的原文（"...open tasks..." / "Plan what to work on
 * next"）。两边本就该是同一份文案，mobile 这份是没有资源可依时手写的；
 * 现在接上资源，以 web 为准即是行为一致性要求（apps/mobile/CLAUDE.md）。
 */
const STARTER_PROMPTS: { icon: string; key: string; fallback: string }[] = [
  {
    icon: "📋",
    key: "starter_prompts.list_open",
    fallback: "List my open tasks by priority",
  },
  {
    icon: "📝",
    key: "starter_prompts.summarize_today",
    fallback: "Summarize what I did today",
  },
  {
    icon: "💡",
    key: "starter_prompts.plan_next",
    fallback: "Plan what to work on next",
  },
];

interface Props {
  hasSessions: boolean;
  agentName?: string;
  onPickPrompt: (text: string) => void;
}

export function ChatEmptyState({ hasSessions, agentName, onPickPrompt }: Props) {
  const { t } = useT("chat");

  // First-time experience: educate before suggesting actions. Starter
  // prompts here would presume the user already knows what chat is for.
  if (!hasSessions) {
    return (
      <View className="flex-1 items-center justify-center px-6 py-8">
        <View className="max-w-xs items-center gap-3">
          <Text className="text-base font-semibold text-foreground text-center">
            {t("empty_state.first_time_title", "Chat with your agents")}
          </Text>
          <Text className="text-sm text-muted-foreground text-center">
            {/* 三段分开是为了让 pillars 那截保留独立字号/字重，不能整句
                插值。中日韩的语序与英文一致（「它们了解你的工作区——任务、
                项目、skill。」），拆句不会造成语序错乱。 */}
            <Text className="text-sm text-muted-foreground">
              {/* 破折号与 pillars 之间的空格由译文自己带（en/ja/ko 末尾
                  有一个空格，zh-Hans 的全角「——」自带间距所以没有）。
                  这里不再写 {" "} 硬空格——它对 zh 会多出一个可见的缝，
                  而按语言判断要不要加空格属于把排版规则写进代码。 */}
              {t(
                "empty_state.first_time_intro",
                "✨ They know your workspace — ",
              )}
            </Text>
            <Text className="text-sm font-medium text-foreground">
              {t("empty_state.first_time_pillars", "issues, projects, skills")}
            </Text>
            <Text className="text-sm text-muted-foreground">
              {t("empty_state.first_time_pillars_suffix", ".")}
            </Text>
          </Text>
          <Text className="text-sm text-muted-foreground text-center">
            {t(
              "empty_state.first_time_actions",
              "Ask for a summary, plan your day, or hand off a quick task.",
            )}
          </Text>
        </View>
      </View>
    );
  }

  // Returning user: starter prompts are the fastest path back to action.
  const title = agentName
    ? t("empty_state.returning_title_named", "Hi, I'm {{name}}", {
        name: agentName,
      })
    : t("empty_state.returning_title_default", "Welcome to Multica");
  return (
    <View className="flex-1 items-center justify-center px-6 py-8 gap-5">
      <View className="items-center gap-1">
        <Text className="text-base font-semibold text-foreground text-center">
          {title}
        </Text>
        <Text className="text-sm text-muted-foreground text-center">
          {t("empty_state.returning_subtitle", "Try asking")}
        </Text>
      </View>
      <View className="w-full max-w-xs gap-2">
        {STARTER_PROMPTS.map((p) => {
          // 译文既是按钮文案，也是点击后填进输入框的草稿内容 —— 用户看到
          // 中文提示、点了却填进英文原文会很突兀，两处必须同一个值。
          const label = t(p.key, p.fallback);
          return (
            <Button
              key={p.key}
              variant="outline"
              onPress={() => onPickPrompt(label)}
              className="h-auto justify-start px-3 py-2.5"
              accessibilityLabel={label}
            >
              <Text className="text-sm text-foreground">
                <Text className="text-sm">{p.icon}  </Text>
                {label}
              </Text>
            </Button>
          );
        })}
      </View>
    </View>
  );
}
