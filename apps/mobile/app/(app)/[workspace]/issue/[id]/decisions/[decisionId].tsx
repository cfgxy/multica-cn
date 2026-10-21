import { ActivityIndicator, ScrollView, View } from "react-native";
import { Stack, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { issueDecisionsOptions } from "@/data/queries/decisions";
import { DecisionCard } from "@/components/issue/decision-card";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

export default function IssueDecisionDetail() {
  const { id, decisionId } = useLocalSearchParams<{ id: string; decisionId: string }>();
  const { t } = useT("issues");
  const decisions = useQuery(issueDecisionsOptions(id));
  const decision = decisions.data?.find((item) => item.id === decisionId);

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen options={{ title: t("decisions.title", "Decision card") }} />
      {decisions.isLoading ? <ActivityIndicator className="mt-8" /> : decisions.error ? (
        <Text className="p-5 text-center text-destructive">{t("decisions.load_failed", "Failed to load decision cards")}</Text>
      ) : decision ? (
        <ScrollView contentContainerClassName="py-4">
          <DecisionCard decision={decision} />
        </ScrollView>
      ) : (
        <Text className="p-5 text-center text-muted-foreground">{t("decisions.not_found", "Decision card not found")}</Text>
      )}
    </View>
  );
}
