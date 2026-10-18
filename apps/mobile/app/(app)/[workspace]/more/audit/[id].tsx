import { ScrollView, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { useQueryClient, type InfiniteData } from "@tanstack/react-query";
import type { AuditEvent, AuditEventsPage } from "@multica/core/types";
import { SheetHeader } from "@/components/ui/sheet-header";
import { Text } from "@/components/ui/text";
import { useWorkspaceStore } from "@/data/workspace-store";
import { auditKeys } from "@/data/queries/audit";
import { safeAuditDetails } from "@/lib/audit-display";
import { useT } from "@/lib/use-t";

export default function AuditDetailRoute() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const workspaceId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const queryClient = useQueryClient();
  const { t } = useT("layout");
  const cachedPages = queryClient.getQueriesData<InfiniteData<AuditEventsPage>>({
    queryKey: auditKeys.all(workspaceId ?? ""),
  });
  const event = cachedPages
    .flatMap(([, data]) => data?.pages.flatMap((page) => page.events) ?? [])
    .find((candidate) => candidate.id === id) ?? null;

  return <View className="flex-1 bg-background">
    <SheetHeader className="flex-row items-center justify-between px-4 pb-3" gap={16}>
      <Text className="text-base font-semibold text-foreground">{t("mobile.audit.detail", "Audit event")}</Text>
      <Text onPress={() => router.back()} className="text-sm text-primary">{t("mobile.audit.close", "Close")}</Text>
    </SheetHeader>
    {event ? <AuditDetails event={event} /> : <Text className="p-5 text-sm text-muted-foreground">{t("mobile.audit.detail_unavailable", "This audit event is no longer available in the current results.")}</Text>}
  </View>;
}

function AuditDetails({ event }: { event: AuditEvent }) {
  const { t } = useT("layout");
  return <ScrollView className="flex-1" contentContainerClassName="gap-3 px-5 pb-6">
    <DetailField label={t("mobile.audit.event_id", "Event ID")} value={event.id} />
    <DetailField label={t("mobile.audit.domain", "Domain")} value={event.domain} />
    <DetailField label={t("mobile.audit.event_type", "Event")} value={event.event_type} />
    <DetailField label={t("mobile.audit.time", "Time")} value={event.occurred_at ?? "-"} />
    <DetailField label={t("mobile.audit.actor", "Actor")} value={`${event.actor_type}${event.actor_id ? ` · ${event.actor_id}` : ""}`} />
    <DetailField label={t("mobile.audit.reason", "Reason")} value={event.reason ?? "-"} />
    {safeAuditDetails(event).map(([label, value]) => <DetailField key={label} label={label} value={value} />)}
  </ScrollView>;
}

function DetailField({ label, value }: { label: string; value: string }) {
  return <View className="gap-1"><Text className="text-xs font-medium text-muted-foreground">{label}</Text><Text selectable className="text-sm text-foreground">{value}</Text></View>;
}
