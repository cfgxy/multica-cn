/**
 * iOS-style large title header. Sits at the top of each tab, above the list.
 * Not a real UINavigationBar — it's a static large title (no scroll-to-shrink
 * collapse), but visually communicates "this is an iOS app" instead of the
 * naked SafeAreaView default.
 */
import { View } from "react-native";
import { Text } from "@/components/ui/text";

export function ScreenHeader({
  title,
  subtitle,
}: {
  title: string;
  subtitle?: string;
}) {
  return (
    <View className="px-4 pt-2 pb-3">
      <Text className="text-3xl font-bold text-foreground">{title}</Text>
      {subtitle ? (
        <Text className="text-sm text-muted-foreground mt-0.5">
          {subtitle}
        </Text>
      ) : null}
    </View>
  );
}
