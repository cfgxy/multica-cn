/**
 * Project priority picker. Single-select over 5 ProjectPriority enum values.
 * Shell mirrors project-status-picker-sheet.tsx.
 */
import { Modal, Pressable, View } from "react-native";
import type { ProjectPriority } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ProjectPriorityIcon } from "@/components/ui/project-priority-icon";
import {
  PROJECT_PRIORITIES,
  PROJECT_PRIORITY_LABEL,
} from "@/lib/project-status";
import { cn } from "@/lib/utils";

interface Props {
  visible: boolean;
  value: ProjectPriority | string;
  onChange: (next: ProjectPriority) => void;
  onClose: () => void;
}

export function ProjectPriorityPickerSheet({
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
              {PROJECT_PRIORITIES.map((priority) => {
                const selected = priority === value;
                return (
                  <Pressable
                    key={priority}
                    onPress={() => {
                      onChange(priority);
                      onClose();
                    }}
                    className={cn(
                      "flex-row items-center gap-3 rounded-lg px-3 py-2.5 active:bg-secondary",
                      selected && "bg-secondary",
                    )}
                  >
                    <ProjectPriorityIcon priority={priority} size={18} />
                    <Text className="flex-1 text-sm text-foreground">
                      {PROJECT_PRIORITY_LABEL[priority]}
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
