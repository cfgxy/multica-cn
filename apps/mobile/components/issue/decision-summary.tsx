import { View } from "react-native";
import type { IssueDecision } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Card } from "@/components/ui/card";
import { useT } from "@/lib/use-t";

export function DecisionSummary({ decision }: { decision: IssueDecision }) {
  const { t } = useT("issues");
  const status = decision.status === "open"
    ? t("decisions.status_open", "Pending")
    : decision.status === "answered"
      ? t("decisions.status_answered", "Answered")
      : t("decisions.status_cancelled", "Cancelled");

  return (
    <Card className="mx-4 flex-row items-center gap-3 px-3 py-3">
      <View className="min-w-0 flex-1">
        <Text numberOfLines={2} className="text-sm font-medium">{decision.question}</Text>
      </View>
      <Text className="shrink-0 rounded-full bg-secondary px-2 py-1 text-xs text-secondary-foreground">{status}</Text>
    </Card>
  );
}

export function DecisionBatchEntry({ openCount }: { openCount: number }) {
  const { t } = useT("issues");
  return (
    <Card className="mx-4 flex-row items-center justify-between gap-3 px-3 py-3">
      <View className="min-w-0 flex-1">
        <Text className="text-sm font-medium">{t("decisions.batch_title", "Batch answer")}</Text>
        <Text className="text-xs text-muted-foreground">{t("decisions.batch_count", "{{count}} pending decisions", { count: openCount })}</Text>
      </View>
      <Text className="text-sm font-semibold text-brand">{t("decisions.start_batch", "Start")}</Text>
    </Card>
  );
}
