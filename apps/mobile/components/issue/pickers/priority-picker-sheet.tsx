/**
 * Issue priority picker. Single-select over the 5 priority enum values.
 * `none` is presented as "No priority" (matches the schema — none is a
 * priority value, not absence of one).
 */
import { Modal, Pressable, View } from "react-native";
import type { IssuePriority } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { cn } from "@/lib/utils";

const PRIORITY_OPTIONS: { value: IssuePriority; label: string }[] = [
  { value: "urgent", label: "Urgent" },
  { value: "high", label: "High" },
  { value: "medium", label: "Medium" },
  { value: "low", label: "Low" },
  { value: "none", label: "No priority" },
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
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
    >
      <Pressable className="flex-1 bg-black/40" onPress={onClose}>
        <View className="flex-1 items-center justify-center px-8">
          <Pressable onPress={() => {}} className="w-full max-w-sm">
            <View className="bg-popover rounded-2xl p-2">
              {PRIORITY_OPTIONS.map(({ value: v, label }) => {
                const selected = v === value;
                return (
                  <Pressable
                    key={v}
                    onPress={() => {
                      onChange(v);
                      onClose();
                    }}
                    className={cn(
                      "flex-row items-center gap-3 rounded-lg px-3 py-2.5 active:bg-secondary",
                      selected && "bg-secondary",
                    )}
                  >
                    <PriorityIcon priority={v} size={16} />
                    <Text className="flex-1 text-sm text-foreground">
                      {label}
                    </Text>
                    {selected ? (
                      <Text className="text-xs text-muted-foreground">✓</Text>
                    ) : null}
                  </Pressable>
                );
              })}
            </View>
          </Pressable>
        </View>
      </Pressable>
    </Modal>
  );
}
