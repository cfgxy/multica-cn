import { ActivityIndicator, View } from "react-native";
import { Stack, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { issueDecisionsOptions } from "@/data/queries/decisions";
import { MobileDecisionCardBrowser } from "@/components/issue/decision-card-browser";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

/**
 * RUYI-863: the batch entry is one parameter of the unified browser, not
 * its own submit-all screen (RUYI-471's batch bar is retired here, as on
 * web). It opens the browser pre-filtered to "open" — first pending card
 * — and answering each card (single-card mutation) walks the pending
 * list forward; when it empties the browser shows its done state.
 */
export default function IssueDecisionBatch() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const { t } = useT("issues");
  const decisions = useQuery(issueDecisionsOptions(id));

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen options={{ title: t("decisions.batch_title", "Batch answer") }} />
      {decisions.isLoading ? <ActivityIndicator className="mt-8" /> : decisions.error ? (
        <Text className="p-5 text-center text-destructive">{t("decisions.load_failed", "Failed to load decision cards")}</Text>
      ) : (
        <MobileDecisionCardBrowser
          decisions={decisions.data ?? []}
          initialFilter="open"
        />
      )}
    </View>
  );
}
