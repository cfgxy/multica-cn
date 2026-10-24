import { ActivityIndicator, View } from "react-native";
import { Stack, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { issueDecisionsOptions } from "@/data/queries/decisions";
import { MobileDecisionCardBrowser } from "@/components/issue/decision-card-browser";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

/**
 * RUYI-863: the single-card deep-link stays available at its old URL
 * (older builds and any stored link) and renders the SAME unified
 * browser pinned to the target card — no second card view, matching the
 * info-feed entry's "pin the card, don't bounce" semantics.
 */
export default function IssueDecisionDetail() {
  const { id, decisionId } = useLocalSearchParams<{
    id: string;
    decisionId: string;
  }>();
  const { t } = useT("issues");
  const decisions = useQuery(issueDecisionsOptions(id));

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen options={{ title: t("decisions.title", "Decision card") }} />
      {decisions.isLoading ? <ActivityIndicator className="mt-8" /> : decisions.error ? (
        <Text className="p-5 text-center text-destructive">{t("decisions.load_failed", "Failed to load decision cards")}</Text>
      ) : (
        <MobileDecisionCardBrowser
          decisions={decisions.data ?? []}
          initialFilter="all"
          initialCardId={decisionId}
        />
      )}
    </View>
  );
}
