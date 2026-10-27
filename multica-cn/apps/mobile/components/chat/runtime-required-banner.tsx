import { Ionicons } from "@expo/vector-icons";
import { View } from "react-native";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

export function RuntimeRequiredBanner({ agentName }: { agentName?: string }) {
  const { t } = useT("chat");
  // 兜底名句首出现，与 web 的 runtime_required_banner.fallback_name（'The agent'）
  // 语义一致但 mobile 用 'This agent'，故用 mobile 专属键。
  const name =
    agentName?.trim() ||
    t("mobile.runtime_required_banner.fallback_name", "This agent");
  return (
    <View className="mx-3 mb-1.5 flex-row items-center gap-1.5 rounded-md bg-warning/15 px-2.5 py-1.5">
      <Ionicons name="server-outline" size={14} color="#a16207" />
      <Text className="flex-1 text-xs text-warning">
        {/* web 的 runtime_required_banner.message 只说 "before it can reply"，
            mobile 还额外指引「去 web 或桌面端绑定」——语义不同，不复用。 */}
        {t(
          "mobile.runtime_required_banner.message",
          "{{name}} needs a runtime before it can run. Bind one on web or desktop.",
          { name },
        )}
      </Text>
    </View>
  );
}
