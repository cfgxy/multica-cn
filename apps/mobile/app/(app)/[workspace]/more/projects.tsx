import { View } from "react-native";
import { Text } from "@/components/ui/text";

/**
 * Projects browse page (placeholder). Read-only list of workspace projects,
 * filled in a later phase. Title comes from Stack.Screen options in
 * `[workspace]/_layout.tsx`.
 */
export default function ProjectsPage() {
  return (
    <View className="flex-1 items-center justify-center bg-background px-6">
      <Text className="text-sm text-muted-foreground text-center">
        Projects coming soon.
      </Text>
    </View>
  );
}
