/**
 * Description block. Renders markdown via the standalone mobile markdown
 * renderer at apps/mobile/lib/markdown/. Empty / null descriptions show
 * a muted "No description." placeholder rather than collapsing the block,
 * so the layout above the timeline stays stable when the user adds a
 * description later.
 */
import { View } from "react-native";
import { Text } from "@/components/ui/text";
import { Markdown } from "@/lib/markdown";

export function IssueDescription({
  description,
}: {
  description: string | null;
}) {
  if (!description || description.trim().length === 0) {
    return (
      <View className="px-4 pb-4">
        <Text className="text-sm text-muted-foreground italic">
          No description.
        </Text>
      </View>
    );
  }
  return (
    <View className="px-4 pb-4">
      <Markdown content={description} />
    </View>
  );
}
