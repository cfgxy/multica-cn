import { ActivityIndicator, Pressable, ScrollView, View } from "react-native";
import { Stack, router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { issueDecisionsOptions } from "@/data/queries/decisions";
import { DecisionSummary } from "@/components/issue/decision-summary";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

export default function IssueDecisions() {
  const { id, workspace } = useLocalSearchParams<{ id: string; workspace: string }>();
  const { t } = useT("issues");
  const decisions = useQuery(issueDecisionsOptions(id));

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen options={{ title: t("decisions.section_all", "Decision cards") }} />
      {decisions.isLoading ? <ActivityIndicator className="mt-8" /> : decisions.error ? (
        <Text className="p-5 text-center text-destructive">{t("decisions.load_failed", "Failed to load decision cards")}</Text>
      ) : (decisions.data?.length ?? 0) === 0 ? (
        <Text className="p-5 text-center text-muted-foreground">{t("decisions.empty_issue", "No decision cards")}</Text>
      ) : (
        <ScrollView contentContainerClassName="gap-3 py-4">
          {decisions.data?.map((decision) => (
            <Pressable key={decision.id} onPress={() => router.push(`/${workspace}/issue/${id}/decisions/${decision.id}`)}>
              <DecisionSummary decision={decision} />
            </Pressable>
          ))}
        </ScrollView>
      )}
    </View>
  );
}
