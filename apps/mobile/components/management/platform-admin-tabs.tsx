import { Pressable, ScrollView } from "react-native";
import { Text } from "@/components/ui/text";

type PlatformAdminTab = {
  value: string;
  label: string;
};

export function PlatformAdminTabs({
  tabs,
  selected,
  onSelect,
}: {
  tabs: readonly PlatformAdminTab[];
  selected: string;
  onSelect: (value: string) => void;
}) {
  return (
    <ScrollView
      testID="platform-admin-tabs"
      horizontal
      showsHorizontalScrollIndicator={false}
      style={{ flexGrow: 0, flexShrink: 0 }}
      contentContainerClassName="gap-2 px-4 py-2"
    >
      {tabs.map(({ value, label }) => (
        <Pressable
          key={value}
          accessibilityRole="tab"
          accessibilityState={{ selected: selected === value }}
          onPress={() => onSelect(value)}
          className={`rounded px-3 py-2 ${selected === value ? "bg-primary" : "bg-secondary"}`}
        >
          <Text className={`text-xs ${selected === value ? "text-primary-foreground" : "text-foreground"}`}>
            {label}
          </Text>
        </Pressable>
      ))}
    </ScrollView>
  );
}
