/**
 * Issue status picker. Single-select over the 7 status enum values
 * (BOARD_STATUSES + cancelled). Mirrors web
 * `packages/views/issues/components/pickers/status-picker.tsx`.
 *
 * Container: iOS pageSheet via shared `<SheetShell>` (CLAUDE.md Lesson #6
 * "picker-row consistency" carve-out — even though this is a short fixed
 * list, the attribute chip row mixes it with assignee/label/project
 * pickers that need pageSheet; using pageSheet everywhere keeps tap →
 * sheet behaviour predictable as the user moves across chips).
 */
import { Pressable, ScrollView, View } from "react-native";
import type { IssueStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { StatusIcon } from "@/components/ui/status-icon";
import { SheetShell } from "@/components/ui/sheet-shell";
import { BOARD_STATUSES, STATUS_LABEL } from "@/lib/issue-status";
import { cn } from "@/lib/utils";

const ALL_STATUSES: IssueStatus[] = [...BOARD_STATUSES, "cancelled"];

interface Props {
  visible: boolean;
  value: IssueStatus;
  onChange: (next: IssueStatus) => void;
  onClose: () => void;
}

export function StatusPickerSheet({
  visible,
  value,
  onChange,
  onClose,
}: Props) {
  return (
    <SheetShell visible={visible} onClose={onClose} title="Status">
      <ScrollView showsVerticalScrollIndicator={false}>
        <View className="px-2 pt-2">
          {ALL_STATUSES.map((status) => {
            const selected = status === value;
            return (
              <Pressable
                key={status}
                onPress={() => {
                  onChange(status);
                  onClose();
                }}
                className={cn(
                  "flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-secondary",
                  selected && "bg-secondary",
                )}
              >
                <StatusIcon status={status} size={18} />
                <Text className="flex-1 text-base text-foreground">
                  {STATUS_LABEL[status]}
                </Text>
                {selected ? (
                  <Text className="text-sm text-muted-foreground">✓</Text>
                ) : null}
              </Pressable>
            );
          })}
        </View>
      </ScrollView>
    </SheetShell>
  );
}
