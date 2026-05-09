/**
 * Mobile PriorityIcon. Renders 4 stacked bars matching the priority order,
 * mirroring the structure of packages/core/issues/config/priority.ts which
 * defines `bars: 0..4` per priority. Visual is mobile-tuned (small chevron
 * stack), but the bar count is the same as web/desktop — Behavioral parity
 * rule (counts and visibility must agree).
 */
import { View } from "react-native";
import { cn } from "@/lib/utils";
import type { IssuePriority } from "@multica/core/types";

const BARS: Record<IssuePriority, number> = {
  urgent: 4,
  high: 3,
  medium: 2,
  low: 1,
  none: 0,
};

const COLOR: Record<IssuePriority, string> = {
  urgent: "bg-destructive",
  high: "bg-warning",
  medium: "bg-warning",
  low: "bg-info",
  none: "bg-muted-foreground/40",
};

export function PriorityIcon({ priority }: { priority: IssuePriority }) {
  const filled = BARS[priority];
  const color = COLOR[priority];

  if (priority === "none") {
    return (
      <View className="size-4 items-center justify-center">
        <View className="size-1 rounded-full bg-muted-foreground/40" />
      </View>
    );
  }

  return (
    <View className="size-4 flex-row items-end justify-center gap-[1px]">
      {[1, 2, 3, 4].map((b) => (
        <View
          key={b}
          className={cn(
            "w-[2px] rounded-sm",
            b <= filled ? color : "bg-muted-foreground/30",
          )}
          style={{ height: 4 + b * 2 }}
        />
      ))}
    </View>
  );
}
