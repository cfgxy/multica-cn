/**
 * Issue priority picker. Single-select over the 5 priority enum values.
 * `none` is presented as "No priority" (matches the schema — none is a
 * priority value, not absence of one). Labels come from the shared
 * source-of-truth in `lib/issue-status.ts` so the picker stays in sync
 * with any future PRIORITY_LABEL edit.
 *
 * Container: iOS pageSheet via shared `<SheetShell>` — see Status picker
 * for the "picker-row consistency" rationale.
 */
import { Pressable, ScrollView, View } from "react-native";
import type { IssuePriority } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { SheetShell } from "@/components/ui/sheet-shell";
import { PRIORITY_LABEL } from "@/lib/issue-status";
import { cn } from "@/lib/utils";

// Display order: severity descending (urgent → none). Distinct from the
// `none, low, medium, high, urgent` enum order in lib/issue-status.ts —
// that order is ascending-by-severity for chart bar counts; we surface
// the more "urgent first" reading here.
const PRIORITY_OPTIONS: IssuePriority[] = [
  "urgent",
  "high",
  "medium",
  "low",
  "none",
];

interface Props {
  visible: boolean;
  value: IssuePriority;
  onChange: (next: IssuePriority) => void;
  onClose: () => void;
}

export function PriorityPickerSheet({
  visible,
  value,
  onChange,
  onClose,
}: Props) {
  return (
    <SheetShell visible={visible} onClose={onClose} title="Priority">
      <ScrollView showsVerticalScrollIndicator={false}>
        <View className="px-2 pt-2">
          {PRIORITY_OPTIONS.map((v) => {
            const selected = v === value;
            return (
              <Pressable
                key={v}
                onPress={() => {
                  onChange(v);
                  onClose();
                }}
                className={cn(
                  "flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-secondary",
                  selected && "bg-secondary",
                )}
              >
                <PriorityIcon priority={v} size={16} />
                <Text className="flex-1 text-base text-foreground">
                  {PRIORITY_LABEL[v]}
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
