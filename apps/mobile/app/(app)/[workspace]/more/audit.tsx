import { useMemo, useState } from "react";
import { ActivityIndicator, FlatList, Modal, Pressable, RefreshControl, ScrollView, TextInput, View } from "react-native";
import { Stack } from "expo-router";
import { useInfiniteQuery } from "@tanstack/react-query";
import type { AuditEvent, ListAuditEventsParams } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { useWorkspaceStore } from "@/data/workspace-store";
import { auditKeys } from "@/data/queries/audit";
import { api } from "@/data/api";
import { auditFailureKind, buildAuditParams, nextAuditCursor, safeAuditDetails, type AuditFilterDraft } from "@/lib/audit-display";
import { useT } from "@/lib/use-t";

const EMPTY_FILTERS: AuditFilterDraft = {
  since: "", until: "", timeBasis: "", actorID: "", actorType: "", clientName: "", clientVersion: "",
  toolName: "", resultStatus: "", resultErrorCode: "", targetType: "", targetID: "",
};

export default function AuditPage() {
  const workspaceId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const { t } = useT("layout");
  const [draft, setDraft] = useState(EMPTY_FILTERS);
  const [applied, setApplied] = useState(EMPTY_FILTERS);
  const [selected, setSelected] = useState<AuditEvent | null>(null);
  const params = useMemo(() => buildAuditParams(applied), [applied]);
  const query = useInfiniteQuery({
    queryKey: auditKeys.list(workspaceId ?? "", params),
    initialPageParam: undefined as Pick<ListAuditEventsParams, "cursor" | "cursor_id"> | undefined,
    queryFn: ({ signal, pageParam }) => api.listAuditEvents(workspaceId ?? "", { ...params, ...pageParam }, { signal }),
    getNextPageParam: (lastPage) => nextAuditCursor(lastPage),
    enabled: workspaceId !== null,
    staleTime: 15_000,
  });
  const events = query.data?.pages.flatMap((page) => page.events) ?? [];

  const update = (key: keyof AuditFilterDraft, value: string) => setDraft((current) => ({ ...current, [key]: value }));
  const renderError = (error: unknown) => {
    const forbidden = auditFailureKind(error) === "permission";
    return <View className="gap-2 p-4">
      <Text className="text-sm text-destructive">{forbidden
        ? t("mobile.audit.permission_denied", "You do not have permission to view workspace audit events.")
        : t("mobile.audit.service_failed", "The audit service is unavailable. Try again.")}</Text>
      <Pressable accessibilityRole="button" onPress={() => query.isFetchNextPageError ? query.fetchNextPage() : query.refetch()} className="self-start rounded bg-secondary px-3 py-2">
        <Text className="text-sm text-foreground">{t("mobile.audit.retry", "Retry")}</Text>
      </Pressable>
    </View>;
  };

  return <View className="flex-1 bg-background">
    <Stack.Screen options={{ title: t("nav.audit", "MCP audit") }} />
    <ScrollView className="max-h-[54%]" keyboardShouldPersistTaps="handled">
      <View className="gap-2 px-4 py-3">
        <Text className="text-xs font-medium text-muted-foreground">{t("mobile.audit.time_filters", "Time")}</Text>
        <FilterInput label={t("mobile.audit.since", "Since (RFC3339)")} value={draft.since} onChange={(v) => update("since", v)} />
        <FilterInput label={t("mobile.audit.until", "Until (RFC3339)")} value={draft.until} onChange={(v) => update("until", v)} />
        <FilterInput label={t("mobile.audit.time_basis", "Time basis (invocation_start)")} value={draft.timeBasis} onChange={(v) => update("timeBasis", v)} />
        <Text className="pt-2 text-xs font-medium text-muted-foreground">{t("mobile.audit.user_filters", "User")}</Text>
        <FilterInput label={t("mobile.audit.actor_id", "Actor ID")} value={draft.actorID} onChange={(v) => update("actorID", v)} />
        <FilterInput label={t("mobile.audit.actor_type", "Actor type")} value={draft.actorType} onChange={(v) => update("actorType", v)} />
        <Text className="pt-2 text-xs font-medium text-muted-foreground">{t("mobile.audit.client_filters", "Client")}</Text>
        <FilterInput label={t("mobile.audit.client_name", "Client name")} value={draft.clientName} onChange={(v) => update("clientName", v)} />
        <FilterInput label={t("mobile.audit.client_version", "Client version")} value={draft.clientVersion} onChange={(v) => update("clientVersion", v)} />
        <Text className="pt-2 text-xs font-medium text-muted-foreground">{t("mobile.audit.tool", "Tool")} / {t("mobile.audit.result", "Result")} / {t("mobile.audit.target", "Target")}</Text>
        <FilterInput label={t("mobile.audit.tool", "Tool")} value={draft.toolName} onChange={(v) => update("toolName", v)} />
        <FilterInput label={t("mobile.audit.result_status", "Result status (success/failure/refused)")} value={draft.resultStatus} onChange={(v) => update("resultStatus", v)} />
        <FilterInput label={t("mobile.audit.result_error_code", "Result error code")} value={draft.resultErrorCode} onChange={(v) => update("resultErrorCode", v)} />
        <FilterInput label={t("mobile.audit.target_type", "Target type (workspace/issue/host)")} value={draft.targetType} onChange={(v) => update("targetType", v)} />
        <FilterInput label={t("mobile.audit.target_id", "Target ID")} value={draft.targetID} onChange={(v) => update("targetID", v)} />
        <Pressable accessibilityRole="button" onPress={() => setApplied({ ...draft })} className="self-start rounded bg-primary px-3 py-2">
          <Text className="text-sm text-primary-foreground">{t("mobile.audit.apply_filters", "Apply filters")}</Text>
        </Pressable>
      </View>
    </ScrollView>
    {query.isLoading ? <View className="flex-1 items-center justify-center"><ActivityIndicator /></View> : null}
    {query.isError && events.length === 0 ? renderError(query.error) : null}
    {query.isError && events.length > 0 && !query.isFetchNextPageError ? renderError(query.error) : null}
    {query.data ? <FlatList
      className="flex-1"
      data={events}
      keyExtractor={(item) => item.id}
      refreshControl={<RefreshControl refreshing={query.isRefetching && !query.isFetchingNextPage} onRefresh={() => query.refetch()} />}
      ListEmptyComponent={<View className="p-6"><Text className="text-sm text-muted-foreground">{t("mobile.audit.empty", "No audit events match this filter.")}</Text></View>}
      ItemSeparatorComponent={() => <View className="ml-4 h-px bg-border" />}
      renderItem={({ item }) => <AuditRow event={item} onPress={() => setSelected(item)} />}
      ListFooterComponent={query.isFetchNextPageError
        ? renderError(query.error)
        : query.hasNextPage ? <Pressable accessibilityRole="button" disabled={query.isFetchingNextPage} onPress={() => query.fetchNextPage()} className="items-center p-4">
          {query.isFetchingNextPage ? <ActivityIndicator /> : <Text className="text-sm text-primary">{t("mobile.audit.load_more", "Load more")}</Text>}
        </Pressable> : null}
    /> : null}
    <AuditDetail event={selected} onClose={() => setSelected(null)} />
  </View>;
}

function FilterInput({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return <TextInput accessibilityLabel={label} value={value} onChangeText={onChange} placeholder={label} autoCapitalize="none" autoCorrect={false} className="min-h-10 rounded border border-border px-3 py-2 text-sm text-foreground" />;
}

function AuditRow({ event, onPress }: { event: AuditEvent; onPress: () => void }) {
  const tool = typeof event.details.tool_name === "string" ? event.details.tool_name : event.event_type;
  const status = typeof event.details.result_status === "string" ? event.details.result_status : event.reason ?? "started";
  return <Pressable accessibilityRole="button" accessibilityLabel={`${tool}, ${status}`} onPress={onPress} className="gap-1 px-4 py-3">
    <View className="flex-row justify-between gap-3"><Text className="flex-1 text-sm font-medium text-foreground" numberOfLines={1}>{tool}</Text><Text className="text-xs text-muted-foreground">{status}</Text></View>
    <Text className="text-xs text-muted-foreground" numberOfLines={1}>{event.actor_id ?? event.actor_type} · {event.occurred_at ?? "-"}</Text>
  </Pressable>;
}

function AuditDetail({ event, onClose }: { event: AuditEvent | null; onClose: () => void }) {
  const { t } = useT("layout");
  return <Modal visible={event !== null} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
    <ScrollView className="flex-1 bg-background" contentContainerClassName="gap-3 p-5">
      <View className="flex-row items-center justify-between"><Text className="text-lg font-semibold text-foreground">{t("mobile.audit.detail", "Audit event")}</Text><Pressable accessibilityRole="button" onPress={onClose}><Text className="text-sm text-primary">{t("mobile.audit.close", "Close")}</Text></Pressable></View>
      {event ? <>
        <DetailField label={t("mobile.audit.event_id", "Event ID")} value={event.id} />
        <DetailField label={t("mobile.audit.domain", "Domain")} value={event.domain} />
        <DetailField label={t("mobile.audit.event_type", "Event")} value={event.event_type} />
        <DetailField label={t("mobile.audit.time", "Time")} value={event.occurred_at ?? "-"} />
        <DetailField label={t("mobile.audit.actor", "Actor")} value={`${event.actor_type}${event.actor_id ? ` · ${event.actor_id}` : ""}`} />
        <DetailField label={t("mobile.audit.reason", "Reason")} value={event.reason ?? "-"} />
        {safeAuditDetails(event).map(([label, value]) => <DetailField key={label} label={label} value={value} />)}
      </> : null}
    </ScrollView>
  </Modal>;
}

function DetailField({ label, value }: { label: string; value: string }) {
  return <View className="gap-1"><Text className="text-xs font-medium text-muted-foreground">{label}</Text><Text selectable className="text-sm text-foreground">{value}</Text></View>;
}
