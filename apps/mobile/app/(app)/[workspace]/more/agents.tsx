import { View } from "react-native";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

export default function AgentsPage() {
  const { t } = useT("layout");
  return (
    <View className="flex-1 items-center justify-center bg-background px-6">
      <Text className="text-sm text-muted-foreground text-center">
        {t("mobile.agents.coming_soon", "Agents coming soon.")}
      </Text>
    </View>
  );
}
