/**
 * Mobile StatusIcon. Renders a small colored circle/glyph per IssueStatus.
 * Mirrors the status enum coverage of packages/core/issues/config/status.ts
 * — every status MUST be represented (Behavioral parity: same enums rule).
 */
import { View } from "react-native";
import type { IssueStatus } from "@multica/core/types";

const COLOR: Record<IssueStatus, string> = {
  backlog: "bg-muted-foreground/30",
  todo: "bg-muted-foreground",
  in_progress: "bg-warning",
  in_review: "bg-success",
  done: "bg-info",
  blocked: "bg-destructive",
  cancelled: "bg-muted-foreground/30",
};

export function StatusIcon({ status }: { status: IssueStatus }) {
  return (
    <View className="size-4 items-center justify-center">
      <View className={`size-2.5 rounded-full ${COLOR[status]}`} />
    </View>
  );
}
