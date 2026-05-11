import { View } from "react-native";
import { Text } from "@/components/ui/text";

/**
 * Quick-create issue modal (placeholder).
 *
 * Wired in via Stack.Screen presentation="modal" in [workspace]/_layout.tsx.
 * Opens from the global header `+` button (HeaderActions). Form + submit
 * land in a later phase.
 */
export default function NewIssueModal() {
  return (
    <View className="flex-1 items-center justify-center bg-background px-6">
      <Text className="text-sm text-muted-foreground text-center">
        New issue form coming soon.
      </Text>
    </View>
  );
}
