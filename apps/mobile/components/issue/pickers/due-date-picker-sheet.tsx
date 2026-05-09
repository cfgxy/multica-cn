/**
 * Due-date picker. Wraps `@react-native-community/datetimepicker` (native
 * UIDatePicker on iOS, Material spinner on Android). Two affordances:
 *   - "Done" — sends the currently displayed date as ISO date string
 *   - "Clear due date" — sends null (only shown when value is set)
 *
 * Date is stored as ISO date-string (YYYY-MM-DD) on the server. The
 * native picker hands us a JS `Date`; we slice the ISO string at the day.
 */
import { useState, useEffect } from "react";
import { Modal, Pressable, View } from "react-native";
import DateTimePicker from "@react-native-community/datetimepicker";
import { Text } from "@/components/ui/text";

interface Props {
  visible: boolean;
  value: string | null;
  onChange: (next: string | null) => void;
  onClose: () => void;
}

function isoToDate(iso: string | null): Date {
  if (!iso) return new Date();
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? new Date() : d;
}

function dateToIsoDay(d: Date): string {
  return d.toISOString().slice(0, 10);
}

export function DueDatePickerSheet({
  visible,
  value,
  onChange,
  onClose,
}: Props) {
  const [draft, setDraft] = useState<Date>(() => isoToDate(value));

  // Reset draft to incoming value when sheet (re)opens.
  useEffect(() => {
    if (visible) setDraft(isoToDate(value));
  }, [visible, value]);

  const submit = () => {
    onChange(dateToIsoDay(draft));
    onClose();
  };
  const clear = () => {
    onChange(null);
    onClose();
  };

  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
    >
      <Pressable className="flex-1 bg-black/40" onPress={onClose}>
        <View className="flex-1 items-center justify-center px-6">
          <Pressable onPress={() => {}} className="w-full max-w-sm">
            <View className="bg-popover rounded-2xl p-4 gap-3">
              <DateTimePicker
                value={draft}
                mode="date"
                display="inline"
                onChange={(_event, selected) => {
                  if (selected) setDraft(selected);
                }}
              />
              <View className="flex-row gap-2 justify-end">
                {value ? (
                  <Pressable
                    onPress={clear}
                    className="px-3 py-2 rounded-md active:bg-secondary"
                  >
                    <Text className="text-sm text-destructive">Clear</Text>
                  </Pressable>
                ) : null}
                <Pressable
                  onPress={onClose}
                  className="px-3 py-2 rounded-md active:bg-secondary"
                >
                  <Text className="text-sm text-muted-foreground">Cancel</Text>
                </Pressable>
                <Pressable
                  onPress={submit}
                  className="px-3 py-2 rounded-md bg-primary active:opacity-80"
                >
                  <Text className="text-sm text-primary-foreground">Done</Text>
                </Pressable>
              </View>
            </View>
          </Pressable>
        </View>
      </Pressable>
    </Modal>
  );
}
