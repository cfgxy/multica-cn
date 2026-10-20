import { ActivityIndicator, Linking, Pressable, RefreshControl, ScrollView, View } from "react-native";
import { Stack } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { useWorkspaceStore } from "@/data/workspace-store";
import { getWebUrl } from "@/data/server-store";
import { larkInstallationsOptions, slackInstallationsOptions, dingTalkInstallationsOptions, wecomInstallationsOptions, telegramInstallationsOptions } from "@/data/queries/integrations";
import { composioConnectionsOptions } from "@/data/queries/composio";
import { useT } from "@/lib/use-t";

export default function IntegrationsPage() {
  const { t } = useT("settings");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const lark = useQuery(larkInstallationsOptions(wsId));
  const slack = useQuery(slackInstallationsOptions(wsId));
  const ding = useQuery(dingTalkInstallationsOptions(wsId));
  const wecom = useQuery(wecomInstallationsOptions(wsId));
  const telegram = useQuery(telegramInstallationsOptions(wsId));
  const composio = useQuery(composioConnectionsOptions());
  const groups = [
    { name: "Lark", query: lark }, { name: "Slack", query: slack },
    { name: "DingTalk", query: ding }, { name: "WeCom", query: wecom },
    { name: "Telegram", query: telegram },
  ];
  const refresh = () => { void Promise.all([lark.refetch(), slack.refetch(), ding.refetch(), wecom.refetch(), telegram.refetch(), composio.refetch()]); };
  const loading = groups.some((group) => group.query.isLoading) || composio.isLoading;
  const webUrl = getWebUrl().replace(/\/+$/, "");
  const settingsUrl = webUrl && slug ? `${webUrl}/${encodeURIComponent(slug)}/settings?tab=integrations` : null;
  const activeComposioConnections = (composio.data ?? []).filter((row) => row.status === "active");
  return <View className="flex-1 bg-background">
    <Stack.Screen options={{ title: t("page.tabs.integrations", "Integrations") }} />
    <ScrollView refreshControl={<RefreshControl refreshing={groups.some((g) => g.query.isRefetching) || composio.isRefetching} onRefresh={refresh} />} contentContainerClassName="gap-4 px-4 py-4">
      <Text className="text-sm text-muted-foreground">{t("mobile_management.integrations_read_only", "Connected integrations · read-only")}</Text>
      {loading ? <ActivityIndicator /> : null}
      {groups.map(({ name, query }) => {
        const rows = query.data?.installations ?? [];
        return <View key={name} className="gap-2">
          <Text className="text-base font-semibold text-foreground">{name}</Text>
          {query.isError ? <Pressable accessibilityRole="button" onPress={() => void query.refetch()} className="rounded border border-border p-3"><Text className="text-sm text-destructive">{t("mobile_management.load_failed", "Could not load. Tap to retry.")}</Text></Pressable> : null}
          {rows.map((row, index) => {
            const account = row.team_id || row.tenant_key || row.account_name || row.login || row.bot_user_id || row.bot_open_id || row.app_id;
            return <View key={row.id ?? `${name}-${index}`} className="flex-row items-center justify-between gap-3 border-b border-border py-3">
              <View className="min-w-0 flex-1"><Text className="text-sm font-medium text-foreground">{account || t("mobile_management.connected_account", "Connected account")}</Text><Text className="text-xs text-muted-foreground">{row.status || t("mobile_management.connected", "Connected")}</Text></View>
            </View>;
          })}
          {rows.length === 0 && !query.isLoading && !query.isError ? <Text className="text-sm text-muted-foreground">{t("mobile_management.not_connected", "No connected accounts")}</Text> : null}
        </View>;
      })}
      <View className="gap-2"><Text className="text-base font-semibold text-foreground">{t("mobile_management.composio", "Composio")}</Text>
        {composio.isError ? <Pressable accessibilityRole="button" onPress={() => void composio.refetch()} className="rounded border border-border p-3"><Text className="text-sm text-destructive">{t("mobile_management.load_failed", "Could not load. Tap to retry.")}</Text></Pressable> : null}
        {activeComposioConnections.map((row) => <Text key={row.toolkit_slug} className="border-b border-border py-3 text-sm text-foreground">{row.toolkit_slug} · {t("mobile_management.connected", "Connected")}</Text>)}
        {activeComposioConnections.length === 0 && !composio.isLoading && !composio.isError ? <Text className="text-sm text-muted-foreground">{t("mobile_management.not_connected", "No connected accounts")}</Text> : null}
      </View>
      {settingsUrl ? <Pressable accessibilityRole="link" onPress={() => void Linking.openURL(settingsUrl)} className="self-start rounded bg-secondary px-3 py-2"><Text className="text-sm text-foreground">{t("mobile_management.open_web_integrations", "Open integration settings on web")}</Text></Pressable> : null}
    </ScrollView>
  </View>;
}
