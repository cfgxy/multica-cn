import { useState } from "react";
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, View } from "react-native";
import { Stack } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { autopilotsOptions } from "@/data/queries/management";
import { api } from "@/data/api";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function AutopilotsPage() {
  const { t } = useT("settings");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const query = useQuery(autopilotsOptions(wsId));
  const quota = useQuery({ queryKey: ["autopilot-quota", wsId], queryFn: ({ signal }) => api.getAutopilotQuotaUsage({ signal }), enabled: !!wsId });
  return <View className="flex-1 bg-background">
    <Stack.Screen options={{ title: t("mobile_management.autopilots", "Automations") }} />
    <ScrollView refreshControl={<RefreshControl refreshing={query.isRefetching || quota.isRefetching} onRefresh={() => { void query.refetch(); void quota.refetch(); }} />} contentContainerClassName="gap-3 px-4 py-4">
      <View className="gap-1 border-b border-border pb-3"><Text className="text-sm font-semibold text-foreground">{t("mobile_management.quota", "Quota")}</Text><Text className="text-xs text-muted-foreground">{quota.data?.action === "off" ? t("mobile_management.quota_unlimited", "No quota limit") : `${quota.data?.used ?? 0} / ${quota.data?.limit ?? "—"} · ${quota.data?.reset_at ? new Date(quota.data.reset_at).toLocaleString() : "—"}`}</Text></View>
      {query.isLoading ? <ActivityIndicator /> : null}
      {query.isError || quota.isError ? <Pressable accessibilityRole="button" onPress={() => { void query.refetch(); void quota.refetch(); }} className="rounded border border-border p-3"><Text className="text-sm text-destructive">{t("mobile_management.load_failed", "Could not load. Tap to retry.")}</Text></Pressable> : null}
      {(query.data?.autopilots ?? []).map((item) => <AutopilotRow key={item.id} item={item} wsId={wsId} />)}
      {query.data?.autopilots.length === 0 ? <Text className="text-sm text-muted-foreground">{t("mobile_management.no_autopilots", "No automations")}</Text> : null}
    </ScrollView>
  </View>;
}

function AutopilotRow({ item, wsId }: { item: { id: string; title: string; status: string; description?: string | null; execution_mode: string; trigger_kinds?: string[]; last_run_at?: string | null }; wsId: string | null }) {
  const { t } = useT("settings");
  const [expanded, setExpanded] = useState(false);
  const runs = useQuery({ queryKey: ["autopilot-runs", wsId, item.id], queryFn: ({ signal }) => api.listAutopilotRuns(item.id, { signal }), enabled: expanded && !!wsId });
  return <View className="gap-2 border-b border-border py-3">
    <Pressable accessibilityRole="button" accessibilityState={{ expanded }} onPress={() => setExpanded((value) => !value)} className="gap-1">
      <View className="flex-row items-center justify-between gap-3"><Text className="flex-1 text-sm font-semibold text-foreground">{item.title}</Text><Text className="text-xs text-muted-foreground">{item.status}</Text></View>
      <Text className="text-xs text-muted-foreground">{item.description || ""}</Text>
      <Text className="text-xs text-muted-foreground">{item.execution_mode} · {(item.trigger_kinds ?? []).join(", ") || t("mobile_management.no_triggers", "No active triggers")}</Text>
      <Text className="text-xs text-muted-foreground">{t("mobile_management.last_run", "Last run")}: {item.last_run_at ? new Date(item.last_run_at).toLocaleString() : "—"}</Text>
    </Pressable>
    {expanded ? <View className="gap-2 pl-2"><Text className="text-xs font-medium text-foreground">{t("mobile_management.run_history", "Recent runs")}</Text>{runs.isLoading ? <ActivityIndicator /> : null}{runs.isError ? <Pressable accessibilityRole="button" onPress={() => void runs.refetch()}><Text className="text-xs text-destructive">{t("mobile_management.load_failed", "Could not load. Tap to retry.")}</Text></Pressable> : null}{(runs.data?.runs ?? []).map((run) => <View key={run.id} className="border-l-2 border-border pl-2 py-1"><Text className="text-xs text-foreground">{run.status} · {run.source}</Text><Text className="text-xs text-muted-foreground">{run.completed_at || run.triggered_at || run.created_at}{run.failure_reason ? ` · ${run.failure_reason}` : ""}</Text></View>)}{runs.data?.runs.length === 0 ? <Text className="text-xs text-muted-foreground">{t("mobile_management.no_runs", "No runs")}</Text> : null}</View> : null}
  </View>;
}
