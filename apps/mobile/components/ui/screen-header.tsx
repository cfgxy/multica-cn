/**
 * iOS-style large title header — single row: title on the left, global
 * actions on the right. Not a real UINavigationBar (no scroll-to-shrink
 * collapse), but visually communicates "this is an iOS app".
 *
 * Tab-specific affordances (filter, scope tabs) belong INSIDE the tab
 * body, not in this header — keeps scope levels from mixing.
 */
import type { ReactNode } from "react";
import { View } from "react-native";
import { Text } from "@/components/ui/text";

export function ScreenHeader({
  title,
  subtitle,
  right,
}: {
  title: string;
  subtitle?: string;
  right?: ReactNode;
}) {
  return (
    <View className="flex-row items-center justify-between px-4 pt-2 pb-3">
      <View className="flex-1 pr-2">
        <Text className="text-3xl font-bold text-foreground">{title}</Text>
        {subtitle ? (
          <Text className="text-sm text-muted-foreground mt-0.5">
            {subtitle}
          </Text>
        ) : null}
      </View>
      {right ? (
        <View className="flex-row items-center gap-1">{right}</View>
      ) : null}
    </View>
  );
}
