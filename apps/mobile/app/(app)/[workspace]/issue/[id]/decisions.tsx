import { ActivityIndicator, View } from "react-native";
import { Stack, useGlobalSearchParams, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { issueDecisionsOptions } from "@/data/queries/decisions";
import { MobileDecisionCardBrowser } from "@/components/issue/decision-card-browser";
import type { DecisionCardFilter } from "@multica/core/issues/decision-card-browser";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

/**
 * The unified decision-card browser screen (RUYI-863). Entry parameters
 * travel as query params and differ per caller:
 * - no params → the sidebar more-menu entry: first card, "all".
 * - `?cardId=…` → the info-feed single-card entry: pin that card under
 *   "all" so an answered/cancelled card lands where the user tapped.
 * - `?filter=open` → the batch entry: first pending card; the browser's
 *   advance rule walks the pending list and shows its done state when it
 *   empties.
 *
 * Query params are read through useGlobalSearchParams (useLocalSearchParams
 * only covers the route's dynamic segments). Values are `string | undefined`.
 */
export default function IssueDecisions() {
  const { id, workspace } = useLocalSearchParams<{
    id: string;
    workspace: string;
  }>();
  const { cardId, filter } = useGlobalSearchParams<{
    cardId?: string;
    filter?: string;
  }>();
  const { t } = useT("issues");
  const decisions = useQuery(issueDecisionsOptions(id));

  const initialCardId = cardId;
  const initialFilter: DecisionCardFilter =
    filter === "open" || filter === "answered" || filter === "cancelled"
      ? filter
      : "all";

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen options={{ title: t("decisions.section_all", "Decision cards") }} />
      {decisions.isLoading ? <ActivityIndicator className="mt-8" /> : decisions.error ? (
        <Text className="p-5 text-center text-destructive">{t("decisions.load_failed", "Failed to load decision cards")}</Text>
      ) : (
        <MobileDecisionCardBrowser
          key={`${initialFilter}:${initialCardId ?? ""}`}
          decisions={decisions.data ?? []}
          initialFilter={initialFilter}
          initialCardId={initialCardId}
        />
      )}
    </View>
  );
}
