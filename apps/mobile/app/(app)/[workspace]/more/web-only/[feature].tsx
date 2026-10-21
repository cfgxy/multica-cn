import { Linking, Pressable, View } from "react-native";
import { Stack, useLocalSearchParams } from "expo-router";
import { Text } from "@/components/ui/text";
import { useWorkspaceStore } from "@/data/workspace-store";
import { getWebUrl } from "@/data/server-store";
import { useT } from "@/lib/use-t";

export default function WebOnlyGuidePage() {
  const { feature } = useLocalSearchParams<{ feature: string }>();
  const { t } = useT("settings");
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const webUrl = getWebUrl().replace(/\/+$/, "");
  const billing = feature === "billing";
  const title = billing ? t("page.tabs.billing", "Billing") : t("page.tabs.marketplace", "Marketplace");
  const href = webUrl && slug ? `${webUrl}/${encodeURIComponent(slug)}/${billing ? "billing" : "marketplace"}` : null;
  return <View className="flex-1 justify-center gap-4 bg-background px-6">
    <Stack.Screen options={{ title }} />
    <Text className="text-base text-foreground">{t(billing ? "mobile_management.billing_guide" : "mobile_management.marketplace_guide", billing ? "Billing is managed on the web app." : "Marketplace browsing and installation are available on the web app.")}</Text>
    {href ? <Pressable accessibilityRole="link" onPress={() => void Linking.openURL(href)} className="self-start rounded bg-primary px-4 py-3"><Text className="text-sm text-primary-foreground">{t("mobile_management.open_web", "Open web app")}</Text></Pressable> : null}
  </View>;
}
