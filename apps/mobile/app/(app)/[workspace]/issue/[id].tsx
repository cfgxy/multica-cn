import { View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useLocalSearchParams } from "expo-router";
import { Text } from "@/components/ui/text";

/**
 * Issue detail placeholder. Real detail (description, timeline, comments)
 * lands in a later iteration — for now this exists so Inbox / My Issues row
 * presses have somewhere to push to, with native iOS back gesture working
 * via Expo Router's parent Stack.
 */
export default function IssueDetailPlaceholder() {
  const { id } = useLocalSearchParams<{ id: string }>();
  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <View className="flex-1 items-center justify-center px-6 gap-2">
        <Text className="text-xs uppercase tracking-wider text-muted-foreground">
          Issue
        </Text>
        <Text className="text-xl font-semibold text-foreground">{id}</Text>
        <Text className="text-sm text-muted-foreground mt-2">
          Detail view — coming soon.
        </Text>
      </View>
    </SafeAreaView>
  );
}
