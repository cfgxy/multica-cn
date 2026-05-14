/**
 * Project status picker. Single-select over the 5 ProjectStatus enum values.
 * Tap-to-apply (no confirm step); sheet auto-closes on selection.
 *
 * Modal shell mirrors issue/pickers/status-picker-sheet.tsx — same fade-in
 * centered popover, same tap-outside-to-dismiss behavior, same selected-row
 * styling.
 */
import { Modal, Pressable, View } from "react-native";
import type { ProjectStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ProjectStatusIcon } from "@/components/ui/project-status-icon";
import {
  PROJECT_STATUSES,
  PROJECT_STATUS_LABEL,
} from "@/lib/project-status";
import { cn } from "@/lib/utils";

interface Props {
  visible: boolean;
  value: ProjectStatus | string;
  onChange: (next: ProjectStatus) => void;
  onClose: () => void;
}

export function ProjectStatusPickerSheet({
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
              {PROJECT_STATUSES.map((status) => {
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
                    <ProjectStatusIcon status={status} size={18} />
                    <Text className="flex-1 text-sm text-foreground">
                      {PROJECT_STATUS_LABEL[status]}
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
