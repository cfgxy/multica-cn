/**
 * Issue status picker. Single-select over the 7 status enum values
 * (BOARD_STATUSES + cancelled). Mirrors web
 * `packages/views/issues/components/pickers/status-picker.tsx`.
 *
 * Modal + backdrop pattern follows emoji-picker-sheet.tsx (no new sheet lib).
 */
import { Modal, Pressable, View } from "react-native";
import type { IssueStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { StatusIcon } from "@/components/ui/status-icon";
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
                      "flex-row items-center gap-3 rounded-lg px-3 py-2.5 active:bg-secondary",
                      selected && "bg-secondary",
                    )}
                  >
                    <StatusIcon status={status} size={18} />
                    <Text className="flex-1 text-sm text-foreground">
                      {STATUS_LABEL[status]}
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
