/**
 * Decisions filter sheet (RUYI-530) — formSheet behind the Decision
 * Center's filter button, the decisions counterpart of issues-filter.
 * Self-contained: reads/writes decisions-view-store directly, no callback
 * passing.
 *
 * Two card-owned toggle dimensions (the status window belongs to the TAB
 * pills on the screen itself, the same division as the Tasks tab's locked
 * status hint):
 *   - 仅看有推荐 — the card carries a recommendation;
 *   - 仅看 Agent 发起 — the card was raised by an agent, not a member.
 * Both apply on every TAB; 重置 clears them and keeps the TAB.
 */
import { Pressable, ScrollView, Switch, View } from "react-native";
import { Text } from "@/components/ui/text";
import { useDecisionsViewStore } from "@/data/stores/decisions-view-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function DecisionsFilterRoute() {
  const { t } = useT("decisions");
  const { colorScheme } = useColorScheme();
  const recommendedOnly = useDecisionsViewStore((s) => s.recommendedOnly);
  const agentCreatedOnly = useDecisionsViewStore((s) => s.agentCreatedOnly);
  const hasActive = recommendedOnly || agentCreatedOnly;

  return (
    <View className="flex-1">
      <View className="flex-row items-center justify-between px-4 pt-4 pb-3">
        <Text className="text-base font-semibold text-foreground">
          {t("mobile.filters.title", "Filter")}
        </Text>
        {hasActive ? (
          <Pressable
            onPress={() => useDecisionsViewStore.getState().clearFilters()}
            hitSlop={8}
            className="px-2 py-1 active:opacity-60"
          >
            <Text className="text-sm text-primary font-medium">
              {t("mobile.filters.reset", "Reset")}
            </Text>
          </Pressable>
        ) : null}
      </View>
      <ScrollView className="flex-1" showsVerticalScrollIndicator={false} nestedScrollEnabled>
        <View className="flex-row items-center gap-3 px-4 py-2.5">
          <Text className="flex-1 text-sm text-foreground">
            {t("mobile.filters.recommended_only", "Recommended only")}
          </Text>
          <Switch
            value={recommendedOnly}
            onValueChange={() =>
              useDecisionsViewStore.getState().toggleRecommendedOnly()
            }
            trackColor={{
              false: THEME[colorScheme].secondary,
              true: THEME[colorScheme].primary,
            }}
          />
        </View>
        <View className="flex-row items-center gap-3 px-4 py-2.5">
          <Text className="flex-1 text-sm text-foreground">
            {t("mobile.filters.agent_created_only", "Agent-created only")}
          </Text>
          <Switch
            value={agentCreatedOnly}
            onValueChange={() =>
              useDecisionsViewStore.getState().toggleAgentCreatedOnly()
            }
            trackColor={{
              false: THEME[colorScheme].secondary,
              true: THEME[colorScheme].primary,
            }}
          />
        </View>
      </ScrollView>
    </View>
  );
}
