/**
 * Chat tab (placeholder).
 *
 * Will port the web bottom-right chat widget (chat_sessions resource)
 * to a mobile-native page. Logic mirrors web; UI is mobile-native.
 * Filled in a later phase.
 */
import { View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Text } from "@/components/ui/text";
import { ScreenHeader } from "@/components/ui/screen-header";
import { HeaderActions } from "@/components/ui/app-header-actions";

export default function Chat() {
  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top"]}>
      <ScreenHeader title="Chat" right={<HeaderActions />} />
      <View className="flex-1 items-center justify-center px-6">
        <Text className="text-sm text-muted-foreground text-center">
          Chat coming soon.
        </Text>
      </View>
    </SafeAreaView>
  );
}
