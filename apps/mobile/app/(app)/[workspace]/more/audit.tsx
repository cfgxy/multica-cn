import { useMemo, useState } from "react";
import { ActivityIndicator, FlatList, Platform, Pressable, RefreshControl, ScrollView, TextInput, View } from "react-native";
import DateTimePicker, { DateTimePickerAndroid } from "@react-native-community/datetimepicker";
import { router, Stack } from "expo-router";
import { useInfiniteQuery } from "@tanstack/react-query";
import type { AuditEvent, ListAuditEventsParams } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { useWorkspaceStore } from "@/data/workspace-store";
import { auditKeys } from "@/data/queries/audit";
import { api } from "@/data/api";
import { auditDateToISOString, auditFailureKind, buildAuditParams, nextAuditCursor, type AuditFilterDraft } from "@/lib/audit-display";
import { useT } from "@/lib/use-t";
import { useActionSheet, ActionSheetModal } from "@/components/ui/action-sheet";

const EMPTY_FILTERS: AuditFilterDraft = {
  since: "", until: "", timeBasis: "", actorID: "", actorType: "", clientName: "", clientVersion: "",
  toolName: "", resultStatus: "", resultErrorCode: "", targetType: "", targetID: "",
};

export default function AuditPage() {
  const workspaceId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const { t } = useT("layout");
  const [draft, setDraft] = useState(EMPTY_FILTERS);
  const [applied, setApplied] = useState(EMPTY_FILTERS);
  const [targetFilterError, setTargetFilterError] = useState(false);
  const timeBasisSheet = useActionSheet();
  const actorTypeSheet = useActionSheet();
  const resultSheet = useActionSheet();
  const errorCodeSheet = useActionSheet();
  const targetTypeSheet = useActionSheet();
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

  const update = (key: keyof AuditFilterDraft, value: string) => {
    setDraft((current) => ({ ...current, [key]: value }));
    if (key === "targetType" || key === "targetID") setTargetFilterError(false);
  };
  const applyFilters = () => {
    if (Boolean(draft.targetType.trim()) !== Boolean(draft.targetID.trim())) {
      setTargetFilterError(true);
      return;
    }
    setTargetFilterError(false);
    setApplied({ ...draft });
  };
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
        <AuditDateInput label={t("mobile.audit.since", "Since (RFC3339)")} value={draft.since} onChange={(v) => update("since", v)} />
        <AuditDateInput label={t("mobile.audit.until", "Until (RFC3339)")} value={draft.until} onChange={(v) => update("until", v)} />
        <FilterChoice label={t("mobile.audit.time_basis", "Time basis (invocation_start)")} value={draft.timeBasis} options={["occurred_at", "invocation_start"]} labels={[t("mobile.audit.occurred_at", "Event time"), "invocation_start"]} onPick={(v) => update("timeBasis", v === "occurred_at" ? "" : v)} sheet={timeBasisSheet} />
        <Text className="pt-2 text-xs font-medium text-muted-foreground">{t("mobile.audit.user_filters", "User")}</Text>
        <FilterInput label={t("mobile.audit.actor_id", "Actor ID")} value={draft.actorID} onChange={(v) => update("actorID", v)} />
        <FilterChoice label={t("mobile.audit.actor_type", "Actor type")} value={draft.actorType} options={["", "member", "agent", "system", "daemon"]} labels={[t("mobile.audit.any", "Any"), "member", "agent", "system", "daemon"]} onPick={(v) => update("actorType", v)} sheet={actorTypeSheet} />
        <Text className="pt-2 text-xs font-medium text-muted-foreground">{t("mobile.audit.client_filters", "Client")}</Text>
        <FilterInput label={t("mobile.audit.client_name", "Client name")} value={draft.clientName} onChange={(v) => update("clientName", v)} />
        <FilterInput label={t("mobile.audit.client_version", "Client version")} value={draft.clientVersion} onChange={(v) => update("clientVersion", v)} />
        <Text className="pt-2 text-xs font-medium text-muted-foreground">{t("mobile.audit.tool", "Tool")} / {t("mobile.audit.result", "Result")} / {t("mobile.audit.target", "Target")}</Text>
        <FilterInput label={t("mobile.audit.tool", "Tool")} value={draft.toolName} onChange={(v) => update("toolName", v)} />
        <FilterChoice label={t("mobile.audit.result_status", "Result status (success/failure/refused)")} value={draft.resultStatus} options={["", "success", "failure", "refused"]} labels={[t("mobile.audit.any", "Any"), "success", "failure", "refused"]} onPick={(v) => update("resultStatus", v)} sheet={resultSheet} />
        <FilterChoice label={t("mobile.audit.result_error_code", "Result error code")} value={draft.resultErrorCode} options={["", "invalid_input", "not_found", "permission_denied", "unavailable", "timeout", "internal"]} labels={[t("mobile.audit.any", "Any"), "invalid_input", "not_found", "permission_denied", "unavailable", "timeout", "internal"]} onPick={(v) => update("resultErrorCode", v)} sheet={errorCodeSheet} />
        <FilterChoice label={t("mobile.audit.target_type", "Target type (workspace/issue/host)")} value={draft.targetType} options={["", "workspace", "issue", "host"]} labels={[t("mobile.audit.any", "Any"), "workspace", "issue", "host"]} onPick={(v) => update("targetType", v)} sheet={targetTypeSheet} />
        <FilterInput label={t("mobile.audit.target_id", "Target ID")} value={draft.targetID} onChange={(v) => update("targetID", v)} />
        {targetFilterError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile.audit.target_pair_required", "Choose both a target type and target ID, or clear both.")}</Text> : null}
        <Pressable accessibilityRole="button" onPress={applyFilters} className="self-start rounded bg-primary px-3 py-2">
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
      renderItem={({ item }) => <AuditRow event={item} onPress={() => router.push(`/more/audit/${encodeURIComponent(item.id)}`)} />}
      ListFooterComponent={query.isFetchNextPageError
        ? renderError(query.error)
        : query.hasNextPage ? <Pressable accessibilityRole="button" disabled={query.isFetchingNextPage} onPress={() => query.fetchNextPage()} className="items-center p-4">
          {query.isFetchingNextPage ? <ActivityIndicator /> : <Text className="text-sm text-primary">{t("mobile.audit.load_more", "Load more")}</Text>}
        </Pressable> : null}
    /> : null}
  </View>;
}
function FilterInput({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return <TextInput accessibilityLabel={label} value={value} onChangeText={onChange} placeholder={label} autoCapitalize="none" autoCorrect={false} className="min-h-10 rounded border border-border px-3 py-2 text-sm text-foreground" />;
}

function AuditDateInput({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  const initialDate = value ? new Date(value) : new Date();
  const openAndroid = () => DateTimePickerAndroid.open({ value: initialDate, mode: "date", display: "default", onChange: (_event, date) => {
    if (!date) return;
    DateTimePickerAndroid.open({ value: initialDate, mode: "time", display: "default", onChange: (_timeEvent, time) => {
      if (!time) return;
      date.setHours(time.getHours(), time.getMinutes(), 0, 0);
      onChange(auditDateToISOString(date));
    } });
  } });
  if (Platform.OS === "android") return <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={openAndroid} className="min-h-10 justify-center rounded border border-border px-3 py-2"><Text className="text-sm text-foreground">{value || label}</Text></Pressable>;
  return <View className="gap-1"><Text className="text-xs text-muted-foreground">{label}</Text><DateTimePicker value={initialDate} mode="datetime" display="compact" onChange={(_event, date) => { if (date) onChange(auditDateToISOString(date)); }} /></View>;
}

function FilterChoice({ label, value, options, labels, onPick, sheet }: { label: string; value: string; options: string[]; labels: string[]; onPick: (value: string) => void; sheet: ReturnType<typeof useActionSheet> }) {
  const { t } = useT("layout");
  const selectedLabel = labels[options.indexOf(value)] ?? t("mobile.audit.any", "Any");
  return <>
    <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={() => sheet.show({ title: label, options: [...labels, t("mobile.audit.cancel", "Cancel")], cancelButtonIndex: labels.length, onSelect: (index) => { if (index < options.length) onPick(options[index]); } })} className="min-h-10 flex-row items-center justify-between rounded border border-border px-3 py-2"><Text className="text-sm text-muted-foreground">{label}</Text><Text className="text-sm text-foreground">{selectedLabel}</Text></Pressable>
    <ActionSheetModal {...sheet.modalProps} />
  </>;
}

function AuditRow({ event, onPress }: { event: AuditEvent; onPress: () => void }) {
  const tool = typeof event.details.tool_name === "string" ? event.details.tool_name : event.event_type;
  const status = typeof event.details.result_status === "string" ? event.details.result_status : event.reason ?? "started";
  return <Pressable accessibilityRole="button" accessibilityLabel={`${tool}, ${status}`} onPress={onPress} className="gap-1 px-4 py-3">
    <View className="flex-row justify-between gap-3"><Text className="flex-1 text-sm font-medium text-foreground" numberOfLines={1}>{tool}</Text><Text className="text-xs text-muted-foreground">{status}</Text></View>
    <Text className="text-xs text-muted-foreground" numberOfLines={1}>{event.actor_id ?? event.actor_type} · {event.occurred_at ?? "-"}</Text>
  </Pressable>;
}
