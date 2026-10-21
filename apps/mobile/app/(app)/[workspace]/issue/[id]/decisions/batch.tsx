import { ActivityIndicator, ScrollView, View } from "react-native";
import { Stack, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { issueDecisionsOptions } from "@/data/queries/decisions";
import { getBatchDecisionCandidates } from "@/lib/decision-batch";
import { DecisionBatchBar } from "@/components/issue/decision-batch-bar";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

export default function IssueDecisionBatch() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const { t } = useT("issues");
  const decisions = useQuery(issueDecisionsOptions(id));
  const open = getBatchDecisionCandidates(decisions.data ?? []);

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen options={{ title: t("decisions.batch_title", "Batch answer") }} />
      {decisions.isLoading ? <ActivityIndicator className="mt-8" /> : decisions.error ? (
        <Text className="p-5 text-center text-destructive">{t("decisions.load_failed", "Failed to load decision cards")}</Text>
      ) : open.length > 1 ? (
        <ScrollView contentContainerClassName="py-4">
          <DecisionBatchBar issueId={id} open={open} />
        </ScrollView>
      ) : (
        <Text className="p-5 text-center text-muted-foreground">{t("decisions.batch_unavailable", "There are not enough pending decisions")}</Text>
      )}
    </View>
  );
}
