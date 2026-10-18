import { useMemo, useState } from "react";
import { ActivityIndicator, FlatList, Pressable, RefreshControl, TextInput, View } from "react-native";
import { Stack } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type { AuditEvent } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { useWorkspaceStore } from "@/data/workspace-store";
import { auditEventsOptions } from "@/data/queries/audit";
import { useT } from "@/lib/use-t";

const statuses = ["", "success", "failure", "refused"] as const;

export default function AuditPage() {
  const workspaceId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const { t } = useT("layout");
  const [status, setStatus] = useState<(typeof statuses)[number]>("");
  const [toolName, setToolName] = useState("");
  const [actorID, setActorID] = useState("");
  const [targetID, setTargetID] = useState("");
  const [since, setSince] = useState("");
  const [applied, setApplied] = useState({ toolName: "", actorID: "", targetID: "", since: "" });
  const params = useMemo(() => ({
    limit: 50,
    ...(status ? { result_status: status } : {}),
    ...(applied.toolName ? { tool_name: applied.toolName } : {}),
    ...(applied.actorID ? { actor_id: applied.actorID } : {}),
    ...(applied.targetID ? { target_type: "host" as const, target_id: applied.targetID } : {}),
    ...(applied.since ? { since: applied.since } : {}),
  }), [applied, status]);
  const query = useQuery(auditEventsOptions(workspaceId, params));

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen options={{ title: t("nav.audit", "MCP audit") }} />
      <View className="flex-row gap-2 px-4 py-3">
        {statuses.map((value) => <Pressable key={value || "all"} onPress={() => setStatus(value)} className={`rounded border px-3 py-1.5 ${status === value ? "border-primary bg-secondary" : "border-border"}`}><Text className="text-xs text-foreground">{value || t("mobile.audit.all", "All")}</Text></Pressable>)}
      </View>
      <View className="gap-2 px-4 pb-3">
        <TextInput value={toolName} onChangeText={setToolName} placeholder={t("mobile.audit.tool", "Tool")} className="h-9 rounded border border-border px-3 text-sm text-foreground" />
        <TextInput value={actorID} onChangeText={setActorID} placeholder={t("mobile.audit.actor_id", "Actor ID")} className="h-9 rounded border border-border px-3 text-sm text-foreground" />
        <TextInput value={targetID} onChangeText={setTargetID} placeholder={t("mobile.audit.host_target_id", "Host target ID")} className="h-9 rounded border border-border px-3 text-sm text-foreground" />
        <TextInput value={since} onChangeText={setSince} placeholder={t("mobile.audit.since", "Since (RFC3339)")} autoCapitalize="none" className="h-9 rounded border border-border px-3 text-sm text-foreground" />
        <Pressable onPress={() => setApplied({ toolName: toolName.trim(), actorID: actorID.trim(), targetID: targetID.trim(), since: since.trim() })} className="self-start rounded bg-primary px-3 py-2"><Text className="text-sm text-primary-foreground">{t("mobile.audit.apply_filters", "Apply filters")}</Text></Pressable>
      </View>
      {query.isLoading ? <View className="flex-1 items-center justify-center"><ActivityIndicator /></View> : null}
      {query.isError ? <View className="p-4"><Text className="text-sm text-destructive">{t("mobile.audit.load_failed", "Unable to load audit events.")}</Text></View> : null}
      {query.data ? <FlatList data={query.data.events} keyExtractor={(item) => item.id} refreshControl={<RefreshControl refreshing={query.isRefetching} onRefresh={query.refetch} />} ListEmptyComponent={<View className="p-6"><Text className="text-sm text-muted-foreground">{t("mobile.audit.empty", "No audit events match this filter.")}</Text></View>} ItemSeparatorComponent={() => <View className="ml-4 h-px bg-border" />} renderItem={({ item }) => <AuditRow event={item} />} /> : null}
    </View>
  );
}

function AuditRow({ event }: { event: AuditEvent }) {
  const tool = typeof event.details.tool_name === "string" ? event.details.tool_name : event.event_type;
  const status = typeof event.details.result_status === "string" ? event.details.result_status : event.reason ?? "started";
  return <View className="gap-1 px-4 py-3"><View className="flex-row justify-between gap-3"><Text className="flex-1 text-sm font-medium text-foreground" numberOfLines={1}>{tool}</Text><Text className="text-xs text-muted-foreground">{status}</Text></View><Text className="text-xs text-muted-foreground" numberOfLines={1}>{event.actor_id ?? event.actor_type} · {event.occurred_at ?? "-"}</Text>{event.reason ? <Text className="text-xs text-muted-foreground" numberOfLines={1}>{event.reason}</Text> : null}</View>;
}
