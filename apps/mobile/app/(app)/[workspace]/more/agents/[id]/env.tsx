/**
 * Agent environment sub-screen (`more/agents/[id]/env`, RUYI-346, design §6)
 * — formSheet shell around `EnvEditor`; all reveal/save semantics live there.
 */
import { Pressable, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { agentDetailOptions } from "@/data/queries/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import { EnvEditor } from "@/components/agents/env-editor";
import { useCanManageAgent } from "@/components/agents/use-can-manage-agent";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function AgentEnvScreen() {
  const insets = useSafeAreaInsets();
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const canManage = useCanManageAgent(agent);

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
          accessibilityLabel={t("create_dialog.cancel", "Cancel")}
        >
          <Ionicons
            name="close"
            size={22}
            color={THEME[colorScheme].foreground}
          />
        </Pressable>
        <Text className="flex-1 text-center text-lg font-semibold text-foreground">
          {t("tabs.environment", "Environment")}
        </Text>
        <View className="w-6" />
      </View>
      <EnvEditor
        agentId={agentId}
        canManage={canManage}
        keyCount={agent?.custom_env_key_count}
      />
    </View>
  );
}
