import { View } from "react-native";
import { Text } from "@/components/ui/text";

/**
 * Global search modal (placeholder). Opens from the global header search
 * icon (HeaderActions). Search field + result list land in a later phase.
 */
export default function SearchModal() {
  return (
    <View className="flex-1 items-center justify-center bg-background px-6">
      <Text className="text-sm text-muted-foreground text-center">
        Search coming soon.
      </Text>
    </View>
  );
}
