/**
 * Agent IM integrations sub-screen (`more/agents/[id]/integrations`,
 * RUYI-418 B3) — status mirror of web's agent integrations tab. Five
 * platforms (lark / slack / dingtalk / wecom / telegram), one row each:
 * whether the workspace has the platform configured, and whether an active
 * installation is bound to THIS agent — the same two predicates web's tab
 * derives from the installation listings (agent_id + status === "active").
 *
 * Scope adaptation (delivery-report item): the bind / scan-to-bind flows
 * need an external IM app's camera or the desktop shell, so mobile renders
 * read-only status plus a pointer to web/desktop settings instead of a
 * dead bind button. Row visibility parity is enforced upstream — the detail
 * screen only links here when at least one platform reports configured.
 */
import { Pressable, ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { agentDetailOptions } from "@/data/queries/agents";
import {
  larkInstallationsOptions,
  slackInstallationsOptions,
  dingTalkInstallationsOptions,
  wecomInstallationsOptions,
  telegramInstallationsOptions,
} from "@/data/queries/integrations";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

type PlatformRow = {
  key: string;
  name: string;
  listing: { configured: boolean; installations: { agent_id?: string | null; status?: string }[] } | undefined;
  isLoading: boolean;
};

export default function AgentIntegrationsScreen() {
  const insets = useSafeAreaInsets();
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const lark = useQuery(larkInstallationsOptions(wsId));
  const slack = useQuery(slackInstallationsOptions(wsId));
  const dingtalk = useQuery(dingTalkInstallationsOptions(wsId));
  const wecom = useQuery(wecomInstallationsOptions(wsId));
  const telegram = useQuery(telegramInstallationsOptions(wsId));

  const rows: PlatformRow[] = [
    {
      key: "lark",
      name: t("mobile.integrations.platform_lark", "Lark"),
      listing: lark.data,
      isLoading: lark.isLoading,
    },
    {
      key: "slack",
      name: t("mobile.integrations.platform_slack", "Slack"),
      listing: slack.data,
      isLoading: slack.isLoading,
    },
    {
      key: "dingtalk",
      name: t("mobile.integrations.platform_dingtalk", "DingTalk"),
      listing: dingtalk.data,
      isLoading: dingtalk.isLoading,
    },
    {
      key: "wecom",
      name: t("mobile.integrations.platform_wecom", "WeCom"),
      listing: wecom.data,
      isLoading: wecom.isLoading,
    },
    {
      key: "telegram",
      name: t("mobile.integrations.platform_telegram", "Telegram"),
      listing: telegram.data,
      isLoading: telegram.isLoading,
    },
  ];

  const closeLabel = t("create_dialog.cancel", "Cancel");

  return (
    <View className="flex-1 bg-background">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Pressable
          onPress={() => router.back()}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={closeLabel}
        >
          <Ionicons
            name="close"
            size={22}
            color={THEME[colorScheme].foreground}
          />
        </Pressable>
        <Text className="flex-1 text-center text-lg font-semibold text-foreground">
          {t("tabs.integrations", "Integrations")}
        </Text>
        <View className="w-6" />
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-3 pb-8 gap-3"
      >
        <Text className="text-xs text-muted-foreground leading-4">
          {t(
            "mobile.integrations.manage_hint",
            "Connect and bind IM channels from the web or desktop settings.",
          )}
        </Text>

        <View className="rounded-lg border border-border overflow-hidden">
          {rows.map((row, i) => {
            const bound =
              agent != null &&
              (row.listing?.installations ?? []).some(
                (inst) =>
                  inst.agent_id === agent.id && inst.status === "active",
              );
            const configured = row.listing?.configured === true;
            const status = bound
              ? t("mobile.integrations.bound_here", "Bound to this agent")
              : configured
                ? t("mobile.integrations.connected", "Workspace connected")
                : t("mobile.integrations.not_configured", "Not configured");
            return (
              <View
                key={row.key}
                className={`flex-row items-center gap-3 px-3 py-3 ${
                  i > 0 ? "border-t border-border" : ""
                }`}
              >
                <View className="flex-1 gap-0.5">
                  <Text className="text-sm font-medium text-foreground">
                    {row.name}
                  </Text>
                  <Text
                    className={`text-xs ${
                      bound
                        ? "text-foreground"
                        : configured
                          ? "text-muted-foreground"
                          : "text-muted-foreground/70"
                    }`}
                  >
                    {row.isLoading ? "…" : status}
                  </Text>
                </View>
                {bound ? (
                  <Ionicons
                    name="checkmark-circle"
                    size={18}
                    color={THEME[colorScheme].brand}
                  />
                ) : null}
              </View>
            );
          })}
        </View>
      </ScrollView>
    </View>
  );
}
